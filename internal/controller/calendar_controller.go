package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	almanacv1 "github.com/evoteum/almanac/api/v1"
)

const (
	conditionReady  = "Ready"
	conditionActive = "Active"

	reasonReconciled        = "Reconciled"
	reasonError             = "Error"
	reasonActive            = "Active"
	reasonInactive          = "Inactive"
	reasonCalendarMissing   = "CalendarNotFound"
	reasonSuspended         = "Suspended"
	reasonInvalidTimezone   = "InvalidTimezone"
	reasonConflictingTarget = "ConflictingScaledObject"
)

var (
	scaledObjectGVK = schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObject",
	}
	scaledObjectListGVK = schema.GroupVersionKind{
		Group:   "keda.sh",
		Version: "v1alpha1",
		Kind:    "ScaledObjectList",
	}
)

// CalendarReconciler reconciles one CalendarScale per call. The Calendar is
// the source of "when"; the CalendarScale is the source of "what / how much";
// ScaledObjects are derived output that lives only in the cluster.
type CalendarReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=almanac.evoteum.com,resources=calendars,verbs=get;list;watch
// +kubebuilder:rbac:groups=almanac.evoteum.com,resources=calendars/status,verbs=get
// +kubebuilder:rbac:groups=almanac.evoteum.com,resources=calendarscales,verbs=get;list;watch
// +kubebuilder:rbac:groups=almanac.evoteum.com,resources=calendarscales/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=keda.sh,resources=scaledobjects,verbs=get;list;watch;create;update;patch;delete

func (r *CalendarReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cs almanacv1.CalendarScale
	if err := r.Get(ctx, req.NamespacedName, &cs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	result, reconcileErr := r.reconcile(ctx, &cs)

	if err := r.Status().Update(ctx, &cs); err != nil && !apierrors.IsNotFound(err) {
		if reconcileErr == nil {
			return result, fmt.Errorf("update CalendarScale status: %w", err)
		}
	}
	return result, reconcileErr
}

func (r *CalendarReconciler) reconcile(ctx context.Context, cs *almanacv1.CalendarScale) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if cs.Spec.Suspend {
		if err := r.pruneOwnedScaledObjects(ctx, cs, nil); err != nil {
			r.setReadyFalse(cs, reasonError, err.Error())
			return ctrl.Result{}, err
		}
		apimeta.SetStatusCondition(&cs.Status.Conditions, metav1.Condition{
			Type:               conditionActive,
			Status:             metav1.ConditionFalse,
			Reason:             reasonSuspended,
			Message:            "CalendarScale is suspended",
			ObservedGeneration: cs.Generation,
		})
		apimeta.SetStatusCondition(&cs.Status.Conditions, metav1.Condition{
			Type:               conditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             reasonSuspended,
			Message:            "CalendarScale is suspended; no ScaledObjects managed",
			ObservedGeneration: cs.Generation,
		})
		return ctrl.Result{}, nil
	}

	var cal almanacv1.Calendar
	calMissing := false
	if err := r.Get(ctx, client.ObjectKey{Name: cs.Spec.CalendarName}, &cal); err != nil {
		if !apierrors.IsNotFound(err) {
			r.setReadyFalse(cs, reasonError, err.Error())
			return ctrl.Result{}, fmt.Errorf("get Calendar %q: %w", cs.Spec.CalendarName, err)
		}
		calMissing = true
		log.Info("referenced Calendar not found; pruning any owned ScaledObjects", "calendar", cs.Spec.CalendarName)
	}

	now := time.Now()

	// Validate recurring window timezones up front — an invalid IANA timezone
	// would be passed straight to KEDA, which would silently reject the trigger.
	if !calMissing {
		for _, rec := range cal.Spec.Recurring {
			if _, err := time.LoadLocation(rec.Timezone); err != nil {
				msg := fmt.Sprintf("Calendar %q has invalid timezone %q in a recurring window: %v", cs.Spec.CalendarName, rec.Timezone, err)
				r.setReadyFalse(cs, reasonInvalidTimezone, msg)
				return ctrl.Result{}, nil // human fix required, no point requeuing
			}
		}
	}

	// Build the desired ScaledObject spec for each target. A target is omitted
	// from desired (and its ScaledObject deleted) only if its trigger list is
	// empty, meaning all absolute instances are in the past and there are no
	// recurring windows.
	type soSpec struct {
		target      almanacv1.CalendarScaleTarget
		triggers    []interface{}
		minReplicas int32
	}
	desired := map[string]soSpec{}

	if !calMissing {
		for _, t := range cs.Spec.Targets {
			triggers := buildTriggers(now, cal.Spec.Instances, cal.Spec.Recurring, t.Replicas)
			if len(triggers) == 0 {
				continue
			}
			minReplicas, err := r.deploymentReplicas(ctx, cs.Namespace, t.DeploymentName)
			if err != nil {
				if apierrors.IsNotFound(err) {
					log.Info("target Deployment not found, skipping", "deployment", t.DeploymentName)
					continue
				}
				r.setReadyFalse(cs, reasonError, err.Error())
				return ctrl.Result{}, fmt.Errorf("get Deployment %q: %w", t.DeploymentName, err)
			}
			// minReplicas must not exceed the event target; if deployment.spec.replicas
			// is already at or above the scale-up value, clamp to avoid an invalid spec.
			minReplicas = min(minReplicas, t.Replicas)
			desired[scaledObjectName(cs.Name, t.DeploymentName)] = soSpec{t, triggers, minReplicas}
		}
	}

	// Guard against two CalendarScales targeting the same deployment — KEDA
	// only allows one ScaledObject per deployment. Check before any upserts so
	// we never apply a partial state.
	for _, spec := range desired {
		conflict, err := r.conflictingScaledObjectExists(ctx, cs, spec.target.DeploymentName)
		if err != nil {
			r.setReadyFalse(cs, reasonError, err.Error())
			return ctrl.Result{}, err
		}
		if conflict {
			msg := fmt.Sprintf("another ScaledObject not owned by this CalendarScale already targets Deployment %q", spec.target.DeploymentName)
			r.setReadyFalse(cs, reasonConflictingTarget, msg)
			return ctrl.Result{}, nil
		}
	}

	keepNames := make(map[string]struct{}, len(desired))
	for name := range desired {
		keepNames[name] = struct{}{}
	}
	if err := r.pruneOwnedScaledObjects(ctx, cs, keepNames); err != nil {
		r.setReadyFalse(cs, reasonError, err.Error())
		return ctrl.Result{}, err
	}
	for name, spec := range desired {
		if err := r.upsertScaledObject(ctx, cs, name, spec.target, spec.triggers, spec.minReplicas); err != nil {
			r.setReadyFalse(cs, reasonError, err.Error())
			return ctrl.Result{}, err
		}
	}

	// Active reflects whether an absolute window is currently open. Recurring
	// windows are handled entirely by KEDA and are not evaluated here.
	active := !calMissing && isActive(now, cal.Spec.Instances)
	r.setActiveCondition(cs, calMissing, active, len(desired))
	apimeta.SetStatusCondition(&cs.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonReconciled,
		Message:            "CalendarScale reconciled successfully",
		ObservedGeneration: cs.Generation,
	})

	// Requeue at the next absolute window boundary so we update status and
	// prune any triggers whose End has passed. Recurring windows need no
	// requeue — KEDA manages their activation independently.
	res := ctrl.Result{}
	if !calMissing {
		if dur, ok := durationToNextBoundary(now, cal.Spec.Instances); ok {
			res.RequeueAfter = dur
		}
	}
	return res, nil
}

// buildTriggers returns the KEDA cron triggers for one target's ScaledObject.
//
// Absolute instances are translated to cron expressions of the form
// "MM HH DD month *" and included only while their End is still in the future;
// once they pass they are dropped and the ScaledObject is updated (or deleted
// if no triggers remain). Recurring windows are included unchanged and never
// expire — they are annual by definition.
func buildTriggers(now time.Time, instances []almanacv1.CalendarInstance, recurring []almanacv1.RecurringWindow, replicas int32) []interface{} {
	replicasStr := strconv.Itoa(int(replicas))
	var triggers []interface{}

	for _, inst := range instances {
		if !inst.End.Time.After(now) {
			continue
		}
		triggers = append(triggers, map[string]interface{}{
			"type": "cron",
			"metadata": map[string]interface{}{
				"timezone":        "UTC",
				"start":           timeToCron(inst.Start.Time),
				"end":             timeToCron(inst.End.Time),
				"desiredReplicas": replicasStr,
			},
		})
	}

	for _, rec := range recurring {
		tz := rec.Timezone
		if tz == "" {
			tz = "UTC"
		}
		triggers = append(triggers, map[string]interface{}{
			"type": "cron",
			"metadata": map[string]interface{}{
				"timezone":        tz,
				"start":           rec.Start,
				"end":             rec.End,
				"desiredReplicas": replicasStr,
			},
		})
	}

	return triggers
}

// timeToCron converts an absolute time to a cron expression that fires at
// that minute/hour/day/month every year. Used for absolute CalendarInstances.
func timeToCron(t time.Time) string {
	return fmt.Sprintf("%d %d %d %d *", t.Minute(), t.Hour(), t.Day(), int(t.Month()))
}

// deploymentReplicas returns the current spec.replicas for a Deployment,
// defaulting to 1 (the Kubernetes default) if the field is unset.
func (r *CalendarReconciler) deploymentReplicas(ctx context.Context, namespace, name string) (int32, error) {
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &deploy); err != nil {
		return 0, err
	}
	if deploy.Spec.Replicas == nil {
		return 1, nil
	}
	return *deploy.Spec.Replicas, nil
}

// isActive reports whether any absolute CalendarInstance window is currently
// open, using [start, end) semantics consistent with durationToNextBoundary.
// Recurring windows are not evaluated here — KEDA handles their activation.
func isActive(now time.Time, instances []almanacv1.CalendarInstance) bool {
	for _, inst := range instances {
		if !now.Before(inst.Start.Time) && now.Before(inst.End.Time) {
			return true
		}
	}
	return false
}

func durationToNextBoundary(now time.Time, instances []almanacv1.CalendarInstance) (time.Duration, bool) {
	var next time.Time
	found := false
	consider := func(t time.Time) {
		if t.After(now) && (!found || t.Before(next)) {
			next = t
			found = true
		}
	}
	for _, inst := range instances {
		consider(inst.Start.Time)
		consider(inst.End.Time)
	}
	if !found {
		return 0, false
	}
	return next.Sub(now), true
}

func (r *CalendarReconciler) upsertScaledObject(ctx context.Context, cs *almanacv1.CalendarScale, name string, t almanacv1.CalendarScaleTarget, triggers []interface{}, minReplicas int32) error {
	so := newScaledObject()
	so.SetNamespace(cs.Namespace)
	so.SetName(name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, so, func() error {
		if err := controllerutil.SetControllerReference(cs, so, r.Scheme); err != nil {
			return err
		}
		spec := map[string]interface{}{
			"scaleTargetRef": map[string]interface{}{
				"name": t.DeploymentName,
			},
			"minReplicaCount": int64(minReplicas),
			"maxReplicaCount": int64(t.Replicas),
			"triggers":        triggers,
		}
		return unstructured.SetNestedField(so.Object, spec, "spec")
	})
	if err != nil {
		return fmt.Errorf("upsert ScaledObject %s/%s: %w", so.GetNamespace(), so.GetName(), err)
	}
	return nil
}

func (r *CalendarReconciler) pruneOwnedScaledObjects(ctx context.Context, cs *almanacv1.CalendarScale, keep map[string]struct{}) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(scaledObjectListGVK)
	if err := r.List(ctx, list, client.InNamespace(cs.Namespace)); err != nil {
		return fmt.Errorf("list ScaledObjects: %w", err)
	}
	for i := range list.Items {
		item := &list.Items[i]
		if !ownedBy(item, cs) {
			continue
		}
		if _, ok := keep[item.GetName()]; ok {
			continue
		}
		if err := r.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete ScaledObject %s/%s: %w", item.GetNamespace(), item.GetName(), err)
		}
	}
	return nil
}

func (r *CalendarReconciler) conflictingScaledObjectExists(ctx context.Context, cs *almanacv1.CalendarScale, deploymentName string) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(scaledObjectListGVK)
	if err := r.List(ctx, list, client.InNamespace(cs.Namespace)); err != nil {
		return false, fmt.Errorf("list ScaledObjects: %w", err)
	}
	for i := range list.Items {
		item := &list.Items[i]
		if ownedBy(item, cs) {
			continue
		}
		target, _, _ := unstructured.NestedString(item.Object, "spec", "scaleTargetRef", "name")
		if target == deploymentName {
			return true, nil
		}
	}
	return false, nil
}

func (r *CalendarReconciler) setActiveCondition(cs *almanacv1.CalendarScale, calMissing, active bool, soCount int) {
	cond := metav1.Condition{
		Type:               conditionActive,
		ObservedGeneration: cs.Generation,
	}
	switch {
	case calMissing:
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonCalendarMissing
		cond.Message = fmt.Sprintf("Calendar %q not found", cs.Spec.CalendarName)
	case active:
		cond.Status = metav1.ConditionTrue
		cond.Reason = reasonActive
		cond.Message = fmt.Sprintf("Calendar %q is active; managing %d ScaledObject(s)", cs.Spec.CalendarName, soCount)
	default:
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonInactive
		cond.Message = fmt.Sprintf("Calendar %q has no active windows; %d ScaledObject(s) pre-staged", cs.Spec.CalendarName, soCount)
	}
	apimeta.SetStatusCondition(&cs.Status.Conditions, cond)
}

func (r *CalendarReconciler) setReadyFalse(cs *almanacv1.CalendarScale, reason, msg string) {
	apimeta.SetStatusCondition(&cs.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: cs.Generation,
	})
}

func ownedBy(obj client.Object, owner *almanacv1.CalendarScale) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.UID == owner.UID && ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}

func newScaledObject() *unstructured.Unstructured {
	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	return so
}

func scaledObjectName(scaleName, deploymentName string) string {
	return scaleName + "-" + deploymentName
}

func (r *CalendarReconciler) calendarToScaleRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	cal, ok := obj.(*almanacv1.Calendar)
	if !ok {
		return nil
	}
	var scales almanacv1.CalendarScaleList
	if err := r.List(ctx, &scales); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range scales.Items {
		cs := &scales.Items[i]
		if cs.Spec.CalendarName == cal.Name {
			reqs = append(reqs, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: cs.Namespace, Name: cs.Name},
			})
		}
	}
	return reqs
}

func (r *CalendarReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&almanacv1.CalendarScale{}).
		Watches(&almanacv1.Calendar{}, handler.EnqueueRequestsFromMapFunc(r.calendarToScaleRequests)).
		Owns(newScaledObject()).
		Named("calendarscale").
		Complete(r)
}
