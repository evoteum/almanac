[//]: # (STANDARD README)
[//]: # (https://github.com/RichardLitt/standard-readme)
[//]: # (----------------------------------------------)
[//]: # (Uncomment optional sections as required)
[//]: # (----------------------------------------------)

[//]: # (Title)
[//]: # (Match repository name)
[//]: # (REQUIRED)

# almanac

[//]: # (Banner)
[//]: # (OPTIONAL)
[//]: # (Must not have its own title)
[//]: # (Must link to local image in current repository)



[//]: # (Badges)
[//]: # (OPTIONAL)
[//]: # (Must not have its own title)



[//]: # (Short description)
[//]: # (REQUIRED)
[//]: # (An overview of the intentions of this repo)
[//]: # (Must not have its own title)
[//]: # (Must be less than 120 characters)
[//]: # (Must match GitHub's description)

Kubernetes operator that generates KEDA ScaledObjects from business calendar events

[//]: # (Long Description)
[//]: # (OPTIONAL)
[//]: # (Must not have its own title)
[//]: # (A detailed description of the repo)

Platform teams define named business occasions, such as Black Friday, product launches or festivals, as `Calendar` resources, then declare which Deployments should scale and by how much using `CalendarScale` resources. almanac generates KEDA `ScaledObject`s from those declarations and keeps them in sync. Users never author or interact with `ScaledObject`s directly.

This replaces manual, spreadsheet-driven pre-scaling with a GitOps-native, auditable, cluster-side workflow.

## Table of Contents

[//]: # (REQUIRED)
[//]: # (Managed automatically)
[//]: # (Changes between the TABLE_OF_CONTENTS_START and TABLE_OF_CONTENTS_END markers will be overritten)

[//]: # (TOCGEN_TABLE_OF_CONTENTS_START)

- [Install](#install)
- [Usage](#usage)
- [Documentation](#documentation)
- [Repository Configuration](#repository-configuration)
- [Contributing](#contributing)
- [License](#license)
    - [Code](#code)
    - [Non-code content](#non-code-content)

[//]: # (TOCGEN_TABLE_OF_CONTENTS_END)

[//]: # (## Security)
[//]: # (OPTIONAL)
[//]: # (May go here if it is important to highlight security concerns.)



[//]: # (## Background)
[//]: # (OPTIONAL)
[//]: # (Explain the motivation and abstract dependencies for this repo)



## Install

[//]: # (Explain how to install the thing.)
[//]: # (OPTIONAL IF documentation repo)
[//]: # (ELSE REQUIRED)

[KEDA](https://keda.sh) is bundled as a Helm dependency and installed automatically.

```bash
helm install almanac oci://ghcr.io/evoteum/charts/almanac \
  -n almanac-system --create-namespace \
  --dependency-update
```

If KEDA is already installed in your cluster:

```bash
helm install almanac oci://ghcr.io/evoteum/charts/almanac \
  -n almanac-system --create-namespace \
  --dependency-update \
  --set keda.enabled=false
```

To install from source:

```bash
helm install almanac ./charts/almanac \
  -n almanac-system --create-namespace \
  --dependency-update \
  --set image.repository=your-registry/almanac \
  --set image.tag=0.1.0
```

## Usage
[//]: # (REQUIRED)
[//]: # (Explain what the thing does. Use screenshots and/or videos.)

### Defining a Calendar

A `Calendar` is cluster-scoped and defines the time windows for a business event. Use `instances` for events whose dates change each year (Easter, Cheltenham Festival), and `recurring` for fixed-date annual events (Christmas).

```yaml
# Variable-date event — specify the exact dates each year
apiVersion: almanac.evoteum.com/v1
kind: Calendar
metadata:
  name: teddybear-festival
spec:
  instances:
    - start: "2026-03-10T08:00:00Z"
      end:   "2026-03-14T18:00:00Z"
```

```yaml
# Fixed-date annual event — cron expressions, runs every year automatically
apiVersion: almanac.evoteum.com/v1
kind: Calendar
metadata:
  name: christmas
spec:
  recurring:
    - start: "0 0 24 12 *"
      end:   "0 0 27 12 *"
      timezone: Europe/London
```

### Scaling Deployments

A `CalendarScale` is namespaced and links a `Calendar` to one or more Deployments.

```yaml
apiVersion: almanac.evoteum.com/v1
kind: CalendarScale
metadata:
  name: teddybear-festival
  namespace: payments
spec:
  calendarName: teddybear-festival
  targets:
    - deploymentName: betting-api
      replicas: 50
    - deploymentName: odds-service
      replicas: 30
```

almanac generates a KEDA `ScaledObject` for each target. Outside active windows the Deployment returns to its normal replica count (`deployment.spec.replicas`). The generated `ScaledObject`s are never committed to git — they are cluster-side output, managed entirely by almanac.

### Checking status

```bash
kubectl get calendarscale -A
# NAME                  CALENDAR             ACTIVE   READY   SUSPENDED   AGE
# teddybear-festival   teddybear-festival  True     True    false       2d
```

### Suspending scaling

Set `spec.suspend: true` to temporarily disable a `CalendarScale` without deleting it. almanac removes the managed `ScaledObject`s until suspend is cleared.



[//]: # (Extra sections)
[//]: # (OPTIONAL)
[//]: # (This should not be called "Extra Sections".)
[//]: # (This is a space for ≥0 sections to be included,)
[//]: # (each of which must have their own titles.)



## Documentation

Further documentation is in the [`docs`](docs/) directory.

| Resource        | Scope     | Purpose                                                    |
|-----------------|-----------|------------------------------------------------------------|
| `Calendar`      | Cluster   | Defines the time windows for a business event              |
| `CalendarScale` | Namespace | Binds a Calendar to target Deployments with replica counts |
| `ScaledObject`  | Namespace | Generated output — do not edit directly                    |

**`Calendar` spec fields**

| Field            | Type                 | Description                                          |
|------------------|----------------------|------------------------------------------------------|
| `spec.instances` | `[]CalendarInstance` | Absolute start/end windows for variable-date events  |
| `spec.recurring` | `[]RecurringWindow`  | Cron-expression windows for fixed-date annual events |

**`CalendarScale` spec fields**

| Field               | Type                    | Description                                             |
|---------------------|-------------------------|---------------------------------------------------------|
| `spec.calendarName` | `string`                | Name of the cluster-scoped `Calendar` to reference      |
| `spec.targets`      | `[]CalendarScaleTarget` | Deployments to scale, each with a `replicas` count      |
| `spec.suspend`      | `bool`                  | Pause reconciliation and remove managed `ScaledObject`s |

## Repository Configuration

> [!WARNING]
> This repo is controlled by OpenTofu in the [estate-repos](https://github.com/evoteum/estate-repos) repository.
>
> Manual configuration changes will be overwritten the next time OpenTofu runs.


[//]: # (## API)
[//]: # (OPTIONAL)
[//]: # (Describe exported functions and objects)



[//]: # (## Maintainers)
[//]: # (OPTIONAL)
[//]: # (List maintainers for this repository)
[//]: # (along with one way of contacting them - GitHub link or email.)



[//]: # (## Thanks)
[//]: # (OPTIONAL)
[//]: # (State anyone or anything that significantly)
[//]: # (helped with the development of this project)



## Contributing
[//]: # (REQUIRED)
If you need any help, please log an issue and one of our team will get back to you.

PRs are welcome.


## License
[//]: # (REQUIRED)

### Code

All source code in this repository is licenced under the [GNU Affero General Public License v3.0 (AGPL-3.0)](https://www.gnu.org/licenses/agpl-3.0.en.html). A
copy of this is provided in the [LICENSE](LICENSE).

### Non-code content

All non-code content in this repository, including but not limited to images, diagrams or prose documentation, is
licenced under the
[Creative Commons Attribution-ShareAlike 4.0 International](https://creativecommons.org/licenses/by-sa/4.0/) licence.
