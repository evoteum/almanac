package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	almanacv1 "github.com/evoteum/almanac/api/v1"
)

const (
	testNamespace = "default"
)

var _ = Describe("CalendarReconciler", func() {
	var (
		reconciler *CalendarReconciler
	)

	BeforeEach(func() {
		reconciler = &CalendarReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
		}
	})

	// unique names per test to avoid cross-test interference
	name := func(prefix string) string {
		return fmt.Sprintf("%s-%d", prefix, GinkgoRandomSeed())
	}

	reconcileCS := func(csName string) {
		GinkgoHelper()
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: testNamespace, Name: csName},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	getCS := func(csName string) *almanacv1.CalendarScale {
		GinkgoHelper()
		out := &almanacv1.CalendarScale{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: csName}, out)).To(Succeed())
		return out
	}

	getCondition := func(csName, condType string) *metav1.Condition {
		GinkgoHelper()
		out := getCS(csName)
		for i := range out.Status.Conditions {
			if out.Status.Conditions[i].Type == condType {
				return &out.Status.Conditions[i]
			}
		}
		return nil
	}

	scaledObjectExists := func(csName, deployName string) bool {
		GinkgoHelper()
		so := &unstructured.Unstructured{}
		so.SetGroupVersionKind(scaledObjectGVK)
		err := k8sClient.Get(ctx, types.NamespacedName{
			Namespace: testNamespace,
			Name:      scaledObjectName(csName, deployName),
		}, so)
		if apierrors.IsNotFound(err) {
			return false
		}
		Expect(err).NotTo(HaveOccurred())
		return true
	}

	makeDeployment := func(deployName string, replicas int32) *appsv1.Deployment {
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: deployName, Namespace: testNamespace},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": deployName}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": deployName}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		return d
	}

	cleanupCalendar := func(calName string) {
		c := &almanacv1.Calendar{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: calName}, c); err == nil {
			Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		}
	}

	cleanupCalendarScale := func(csName string) {
		c := &almanacv1.CalendarScale{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: csName}, c); err == nil {
			Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		}
	}

	cleanupDeployment := func(deployName string) {
		d := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: deployName}, d); err == nil {
			Expect(k8sClient.Delete(ctx, d)).To(Succeed())
		}
	}

	Context("when the referenced Calendar does not exist", func() {
		It("sets Active=False with CalendarNotFound and Ready=True", func() {
			csName := name("cs-no-cal")
			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: "does-not-exist",
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: "app", Replicas: 5}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)

			active := getCondition(csName, conditionActive)
			Expect(active).NotTo(BeNil())
			Expect(active.Status).To(Equal(metav1.ConditionFalse))
			Expect(active.Reason).To(Equal(reasonCalendarMissing))

			ready := getCondition(csName, conditionReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("when the Calendar has an active window", func() {
		It("creates a ScaledObject for each target with correct replica counts", func() {
			calName := name("cal-active")
			deployName := name("deploy-active")
			csName := name("cs-active")

			now := time.Now().UTC()
			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Instances: []almanacv1.CalendarInstance{
						{
							Start: metav1.Time{Time: now.Add(-time.Hour)},
							End:   metav1.Time{Time: now.Add(time.Hour)},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			makeDeployment(deployName, 2)
			DeferCleanup(cleanupDeployment, deployName)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: deployName, Replicas: 10}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)

			Expect(scaledObjectExists(csName, deployName)).To(BeTrue())

			so := &unstructured.Unstructured{}
			so.SetGroupVersionKind(scaledObjectGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace,
				Name:      scaledObjectName(csName, deployName),
			}, so)).To(Succeed())

			minReplicas, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
			maxReplicas, _, _ := unstructured.NestedInt64(so.Object, "spec", "maxReplicaCount")
			Expect(minReplicas).To(Equal(int64(2))) // deployment.spec.replicas
			Expect(maxReplicas).To(Equal(int64(10)))

			active := getCondition(csName, conditionActive)
			Expect(active.Status).To(Equal(metav1.ConditionTrue))
			Expect(active.Reason).To(Equal(reasonActive))
		})
	})

	Context("when the Calendar has only past windows", func() {
		It("does not create a ScaledObject and sets Active=False", func() {
			calName := name("cal-past")
			deployName := name("deploy-past")
			csName := name("cs-past")

			past := time.Now().UTC().Add(-24 * time.Hour)
			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Instances: []almanacv1.CalendarInstance{
						{
							Start: metav1.Time{Time: past.Add(-time.Hour)},
							End:   metav1.Time{Time: past},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			makeDeployment(deployName, 3)
			DeferCleanup(cleanupDeployment, deployName)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: deployName, Replicas: 10}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)

			Expect(scaledObjectExists(csName, deployName)).To(BeFalse())

			active := getCondition(csName, conditionActive)
			Expect(active.Status).To(Equal(metav1.ConditionFalse))
			Expect(active.Reason).To(Equal(reasonInactive))
		})
	})

	Context("when minReplicas would exceed target replicas", func() {
		It("clamps minReplicaCount to target replicas", func() {
			calName := name("cal-clamp")
			deployName := name("deploy-clamp")
			csName := name("cs-clamp")

			now := time.Now().UTC()
			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Instances: []almanacv1.CalendarInstance{
						{
							Start: metav1.Time{Time: now.Add(-time.Hour)},
							End:   metav1.Time{Time: now.Add(time.Hour)},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			// deployment has more replicas than the scale target
			makeDeployment(deployName, 20)
			DeferCleanup(cleanupDeployment, deployName)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: deployName, Replicas: 5}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)

			so := &unstructured.Unstructured{}
			so.SetGroupVersionKind(scaledObjectGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace,
				Name:      scaledObjectName(csName, deployName),
			}, so)).To(Succeed())

			minReplicas, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
			maxReplicas, _, _ := unstructured.NestedInt64(so.Object, "spec", "maxReplicaCount")
			Expect(minReplicas).To(Equal(int64(5))) // clamped to target
			Expect(maxReplicas).To(Equal(int64(5)))
		})
	})

	Context("when CalendarScale is suspended", func() {
		It("removes existing ScaledObjects and sets Active=False/Suspended, Ready=True", func() {
			calName := name("cal-suspend")
			deployName := name("deploy-suspend")
			csName := name("cs-suspend")

			now := time.Now().UTC()
			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Instances: []almanacv1.CalendarInstance{
						{
							Start: metav1.Time{Time: now.Add(-time.Hour)},
							End:   metav1.Time{Time: now.Add(time.Hour)},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			makeDeployment(deployName, 2)
			DeferCleanup(cleanupDeployment, deployName)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: deployName, Replicas: 10}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			// First reconcile — ScaledObject should be created
			reconcileCS(csName)
			Expect(scaledObjectExists(csName, deployName)).To(BeTrue())

			// Re-fetch before update — reconcile updated the status, changing ResourceVersion
			current := getCS(csName)
			current.Spec.Suspend = true
			Expect(k8sClient.Update(ctx, current)).To(Succeed())

			reconcileCS(csName)

			Expect(scaledObjectExists(csName, deployName)).To(BeFalse())

			active := getCondition(csName, conditionActive)
			Expect(active.Status).To(Equal(metav1.ConditionFalse))
			Expect(active.Reason).To(Equal(reasonSuspended))

			ready := getCondition(csName, conditionReady)
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		})
	})

	Context("when a target is removed from the CalendarScale", func() {
		It("prunes the orphaned ScaledObject", func() {
			calName := name("cal-prune")
			deployA := name("deploy-prune-a")
			deployB := name("deploy-prune-b")
			csName := name("cs-prune")

			now := time.Now().UTC()
			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Instances: []almanacv1.CalendarInstance{
						{
							Start: metav1.Time{Time: now.Add(-time.Hour)},
							End:   metav1.Time{Time: now.Add(time.Hour)},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			makeDeployment(deployA, 2)
			DeferCleanup(cleanupDeployment, deployA)
			makeDeployment(deployB, 2)
			DeferCleanup(cleanupDeployment, deployB)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets: []almanacv1.CalendarScaleTarget{
						{DeploymentName: deployA, Replicas: 5},
						{DeploymentName: deployB, Replicas: 5},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)
			Expect(scaledObjectExists(csName, deployA)).To(BeTrue())
			Expect(scaledObjectExists(csName, deployB)).To(BeTrue())

			// Remove deployB from targets
			updated := getCS(csName)
			updated.Spec.Targets = []almanacv1.CalendarScaleTarget{
				{DeploymentName: deployA, Replicas: 5},
			}
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())

			reconcileCS(csName)
			Expect(scaledObjectExists(csName, deployA)).To(BeTrue())
			Expect(scaledObjectExists(csName, deployB)).To(BeFalse())
		})
	})

	Context("when a Calendar has an invalid timezone in a recurring window", func() {
		It("sets Ready=False with InvalidTimezone and does not create ScaledObjects", func() {
			calName := name("cal-bad-tz")
			deployName := name("deploy-bad-tz")
			csName := name("cs-bad-tz")

			cal := &almanacv1.Calendar{
				ObjectMeta: metav1.ObjectMeta{Name: calName},
				Spec: almanacv1.CalendarSpec{
					Recurring: []almanacv1.RecurringWindow{
						{Start: "0 9 25 12 *", End: "0 9 27 12 *", Timezone: "Not/ATimezone"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cal)).To(Succeed())
			DeferCleanup(cleanupCalendar, calName)

			makeDeployment(deployName, 2)
			DeferCleanup(cleanupDeployment, deployName)

			cs := &almanacv1.CalendarScale{
				ObjectMeta: metav1.ObjectMeta{Name: csName, Namespace: testNamespace},
				Spec: almanacv1.CalendarScaleSpec{
					CalendarName: calName,
					Targets:      []almanacv1.CalendarScaleTarget{{DeploymentName: deployName, Replicas: 10}},
				},
			}
			Expect(k8sClient.Create(ctx, cs)).To(Succeed())
			DeferCleanup(cleanupCalendarScale, csName)

			reconcileCS(csName)

			Expect(scaledObjectExists(csName, deployName)).To(BeFalse())

			ready := getCondition(csName, conditionReady)
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(reasonInvalidTimezone))
		})
	})
})
