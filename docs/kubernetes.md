# Deploy Kubernetes manifests

A repository that runs on Kubernetes deploys to Gregale from the manifests it
already has. Deployments become apps and CronJobs become scheduled jobs, with
their sizes, healthchecks, process settings and replica limits. Everything
Gregale does not apply is listed in the plan with the reason and, where one
exists, the Gregale setting to use instead.

> **Preview.** Kubernetes manifests are a preview capability, released per
> account. Ask support to enable it; `gregale capabilities` lists it once it is
> on. Behavior can change before general availability.

The import is one way. Gregale reads the files in your repository; it never
connects to a cluster, writes manifests back, or serves a Kubernetes API.

## Deploy

Put the manifests in one of these directories at the repository root:
`k8s/`, `kubernetes/`, `deploy/` or `manifests/`. Then deploy the repository
as a project, as you would a Compose file:

```sh
gregale deploy --diff   # see the plan only
gregale deploy          # plan, review, then deploy
```

A repository connected to GitHub deploys the same way without a checkout:
`gregale deploy --repo octo/shop --ref main --project`. The plan is the import
report: it lists every app it will create, then a **Warnings** section with each
field or object that was not applied and why. Nothing is written to your
repository. [Multi-service apps](multi-service-apps.md) covers projects, service
calls and previews, which work the same for Kubernetes manifests.

Gregale reads YAML files with a string `apiVersion` and `kind`. Other YAML in
those directories, such as an Ansible playbook or a values file, is skipped and
named once in the warnings. A directory holding a Helm chart (`Chart.yaml`) or a
`kustomization.yaml` is skipped with the command to render it:

```sh
helm template shop ./chart > k8s/rendered.yaml
kubectl kustomize overlays/production > k8s/rendered.yaml
```

Commit the rendered output outside the chart or overlay directory. Gregale does
not render charts or overlays. A scan reads at most 500 manifest files, 1,000
documents in one file and 16 MiB in total.

When a Compose file and a manifest declare a workload with the same name,
Compose wins.

## What is applied

### Workloads

| Kubernetes | Gregale |
|---|---|
| `Deployment` | An app. |
| `CronJob` | A scheduled job: `schedule`, `suspend` (a suspended CronJob's cron is disabled) and `timeZone`. |
| `Service` | Not an app. It links its Deployment to its port and to the addresses other workloads call it by. |
| `HorizontalPodAutoscaler` | `maxReplicas` caps the app's instances. |
| `ConfigMap` | Plain values for the [environment import](#environment). |

A Deployment whose image is a known datastore (Postgres, Redis, …) is offered as
the managed resource instead, as in Compose. Each app keeps its Deployment or
CronJob name, or takes `<project>-<name>` when the name is reserved or taken: in
a project `shop`, Deployment `api` becomes the app `shop-api`.

### The app's container

The app runs the container named by the `kubectl.kubernetes.io/default-container`
annotation, or the first one.

| Field | Becomes |
|---|---|
| `command`, `args` | The entrypoint and the command, as Kubernetes runs them. |
| `workingDir` | The working directory. |
| `securityContext.runAsUser`, `runAsGroup` | The process user. |
| `terminationGracePeriodSeconds` | How long shutdown may take. |
| `ports` | The serving port: the one a selecting Service's `targetPort` names, by number or name, or else the first `containerPort`. |
| `resources.limits.memory`, `.cpu` | The app's RAM and CPU. Without a limit the request is used, with a warning: a Gregale instance has one size, which is both its guarantee and its cap. |
| `readinessProbe` | The app's [healthcheck](#healthchecks). |
| `startupProbe` | The healthcheck when there is no readiness probe; otherwise its budget sets how long startup may take. |
| `replicas` | The cap on the app's instances, unless an HPA targets the Deployment. |

Quantities are read exactly (`512Mi`, `1Gi`, `500m`) and rounded as Compose
sizes are. A size over your plan fails the deploy with the limit and the value
you asked for.

### Healthchecks

The readiness probe gates every start: an instance gets traffic only after it
passes. Its `periodSeconds`, `timeoutSeconds`, `failureThreshold` and
`initialDelaySeconds` keep their meaning, with the Kubernetes defaults when
absent.

| Probe action | Applied as |
|---|---|
| `exec.command` | The image healthcheck. |
| `httpGet` | An HTTP check of `path` on the serving port. |
| `grpc` | A gRPC health check of `service` on the serving port. |
| `tcpSocket` | Not needed: Gregale already waits for the serving port. |

Three differences from a cluster:

- **A check that fails after the instance was ready replaces the instance.**
  Kubernetes would take it out of traffic and keep it running. On plans with
  liveness checks (Hobby and above; gRPC on Pro and Scale), the same check is
  the instance's liveness check, at the plan's default timing.
- **HTTP checks pass on 2xx.** Kubernetes also accepts 3xx.
- **The check runs against the serving port, over plain HTTP or gRPC.** A probe
  of another port, or with `scheme: HTTPS`, is reported and not applied.

`livenessProbe` is not imported. The healthcheck above already replaces a
broken instance, and a liveness probe that is stricter than readiness, or that
calls a dependency, is the usual cause of restart loops.

### Replicas and autoscaling

Gregale starts instances on demand and parks them when idle, so replica counts
become limits, not targets:

- `replicas`, or `maxReplicas` when an HPA targets the Deployment, caps how many
  instances run at once.
- `minReplicas` is not imported. To keep instances warm on purpose, set
  `scaling.min_instances` in `gregale.yaml`; warm instances are billed while
  resident.
- An HPA CPU target is reported with the equivalent `scaling.targets` entry. It
  is not applied: Gregale measures each instance's CPU, not its share of a
  request.

### Scheduled jobs

A CronJob runs in its `timeZone`, or UTC without one. A zone you declare
replaces one set on the existing cron with `gregale cron update --timezone`;
without one, the cron keeps its current zone. An unknown zone fails the plan.

`concurrencyPolicy` (`Forbid` or `Replace`), `startingDeadlineSeconds` and the
history limits are reported with the [cron](crons.md) setting to use after the
first deploy: `gregale cron update CRON_ID --skip-if-running`, or the overlap
and start deadline in `--schedule-policy`. `gregale cron runs CRON_ID` lists
every run for the history retention period.

## Environment

The plan names every variable a container sets. Without an opt-in, each one is
listed under **Needs a value**; set them with `gregale env set` or a
`--secrets-file`, as for Compose.

To copy plain values from the manifests, annotate the Deployment or CronJob:

```yaml
metadata:
  name: api
  annotations:
    gregale.dev/env-import: "true"
```

With the annotation, the deploy imports:

- literal `env[].value` entries;
- the values of ConfigMap keys a container reads through
  `env[].valueFrom.configMapKeyRef` or `envFrom[].configMapRef`, when the
  ConfigMap is among the scanned manifests.

The same rules as the [Compose import](multi-service-apps.md#configuration)
apply: a value already set on the app is never overwritten, names that look like
credentials and values that look like secrets are skipped, and the plan shows
names, never values. Imports happen on reviewed deploys (`gregale deploy`, the
dashboard, `--repo … --project`); a GitHub push deploy imports nothing.

A value that calls another scanned Service over HTTP is rewritten to that
service's Gregale address, and the plan names each rewrite. These forms are
recognized, with any port: `http://billing`, `billing.shop`,
`billing.shop.svc` and `billing.shop.svc.cluster.local`.

**Secrets are never imported.** Keys from `secretKeyRef`, `envFrom[].secretRef`
and every `Secret` manifest are listed under **Needs a value**; their values
never reach the plan, logs or audit events. A committed `Secret` with `data` or
`stringData` is flagged: base64 is not encryption, so those values are in your
repository's history and should be rotated.

## Ingress

An Ingress is not applied. Every app already gets an HTTPS URL, and TLS ends at
the Gregale edge. The plan turns each Ingress into notes you can act on:

- **Hosts.** Each host is listed with the `gregale domain create` command that
  adds it. Domains need ownership verification, so the deploy never adds one.
- **Paths to several Services.** Each Service's Deployment becomes its own app
  with its own URL. The plan lists each path and Service; [`route` edge
  rules](edge-rules.md#route) serve them under one host.
- **Proxy settings.** Common annotations are compared with what your plan
  allows, using the numbers your account has at plan time:

| Annotation (ingress-nginx; NGINX, HAProxy and Traefik equivalents too) | The note compares it with |
|---|---|
| `proxy-body-size` | your plan's request body limit |
| `proxy-read-timeout`, `proxy-send-timeout` | the request timeout: its default, and the most `gregale app update --request-timeout` allows on your plan |
| `websocket-services` | whether your plan forwards WebSockets by default, lets you turn them on, or does not offer them |
| `ssl-redirect`, `force-ssl-redirect` | nothing to do: app URLs use HTTPS |

For example, on the Pro plan:

```text
api: Ingress "shop" proxy-body-size 50m: this plan accepts request bodies up to 100 MB
api: Ingress "shop" proxy-read-timeout 120: requests time out after 30 s by default; `gregale app update --request-timeout 120 shop-api` matches it
```

Other annotations, such as rewrites, CORS and rate limits, are not compared.
[Edge rules](edge-rules.md) cover them. Traefik sets body size and timeouts in
Middleware objects, which are not read.

## What is not applied

Each of these is reported in the plan with its reason:

| Object or field | Why, and what to use instead |
|---|---|
| `StatefulSet`, `DaemonSet` | Refused. Apps are stateless, and nodes are Gregale's. Use a managed database, or one you run elsewhere. |
| `Job` | Run one-off work with `gregale job create` and `gregale job run`. |
| `PersistentVolumeClaim`, `PersistentVolume`, `StorageClass`, `volumes`, `volumeMounts` | Apps keep no disk. Use [object storage](object-storage.md) or a managed database; write scratch data under `/tmp`. |
| `NetworkPolicy` | Apps reach each other only through declared service bindings, private and metadata ranges are always blocked, and `gregale app egress-allowlist` narrows outbound traffic. |
| `ServiceAccount`, `Role`, `RoleBinding`, `ClusterRole*` | People and CI use scoped keys and organization roles. A cloud role annotation on a ServiceAccount (EKS, GKE, AKS) points to [workload identity](workload-identity.md). |
| `initContainers` | Not run. A one-time migration belongs in the release command. |
| Sidecar containers | Run them as [companions](companions.md). |
| `livenessProbe` | See [healthchecks](#healthchecks). |
| `lifecycle` | Hooks are not run; `terminationGracePeriodSeconds` sets the shutdown time. |
| `nodeSelector`, `affinity`, `tolerations`, `topologySpreadConstraints`, `priorityClassName` | Gregale places instances. |
| `hostNetwork`, `hostPID`, `privileged` and other kernel controls | Each app runs in its own microVM. |
| `imagePullSecrets` | Set registry credentials with `gregale registry`. |
| Service `type: LoadBalancer`, `NodePort` | Every app already has an HTTPS URL. |

`Namespace`, `PodDisruptionBudget`, `PriorityClass`, `LimitRange` and
`ResourceQuota` are ignored without a warning; a namespace is still used to
match Services to Deployments. Any other kind, custom resources included, is
named in a warning.

A Deployment that no Service selects and that declares no `containerPort` is
reported: it still runs as a request-serving app, so set
`execution_mode: worker` in `gregale.yaml` if it consumes a queue.

## Example

```yaml
# k8s/shop.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  annotations:
    gregale.dev/env-import: "true"
spec:
  replicas: 3
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers:
        - name: api
          image: ghcr.io/acme/api:1.4
          ports: [{containerPort: 8080}]
          resources:
            limits: {memory: 512Mi, cpu: 500m}
          readinessProbe:
            httpGet: {path: /ready, port: 8080}
            periodSeconds: 5
          livenessProbe:
            httpGet: {path: /healthz, port: 8080}
          env:
            - {name: LOG_LEVEL, value: info}
            - name: DATABASE_URL
              valueFrom: {secretKeyRef: {name: api-secrets, key: database-url}}
          envFrom:
            - configMapRef: {name: api-config}
---
apiVersion: v1
kind: Service
metadata: {name: api}
spec:
  selector: {app: api}
  ports: [{port: 80, targetPort: 8080}]
---
apiVersion: v1
kind: ConfigMap
metadata: {name: api-config}
data:
  FEATURE_FLAGS: search,export
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: api}
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: api}
  minReplicas: 2
  maxReplicas: 5
  metrics:
    - type: Resource
      resource: {name: cpu, target: {type: Utilization, averageUtilization: 70}}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: 50m
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - {path: /, pathType: Prefix, backend: {service: {name: api, port: {number: 80}}}}
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly-report}
spec:
  schedule: "0 3 * * *"
  timeZone: Europe/Berlin
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: report
              image: ghcr.io/acme/api:1.4
              command: [node, scripts/report.js]
          restartPolicy: OnFailure
```

This deploys two apps:

- **api:** 512 MB and 500 millicores, at most 5 instances (the HPA's
  `maxReplicas`), healthy once `GET /ready` on port 8080 returns 2xx, checked
  every 5 s. `LOG_LEVEL` and `FEATURE_FLAGS` are imported; `DATABASE_URL` is
  listed under **Needs a value**.
- **nightly-report:** a job that runs `node scripts/report.js` at 03:00
  Berlin time.

The plan's warnings include:

```text
reposcan: k8s/shop.yaml: api: livenessProbe ignored: an instance whose healthcheck fails after it was ready is replaced; a separate liveness check is not used, because a strict one is the usual cause of restart loops
reposcan: k8s/shop.yaml: api: HorizontalPodAutoscaler "api": minReplicas 2 not imported: instances start on demand and park when idle; keep instances warm with scaling.min_instances in gregale.yaml, billed while resident
reposcan: k8s/shop.yaml: kind Ingress "shop" ignored: every app gets an HTTPS URL; add shop.example.com with `gregale domain create --app <app> --domain shop.example.com`
api: Ingress "shop" proxy-body-size 50m: this plan accepts request bodies up to 100 MB
```

Then set the secret and add the domain:

```sh
gregale env set --app shop-api DATABASE_URL=postgres://…
gregale domain create --app shop-api --domain shop.example.com
```
