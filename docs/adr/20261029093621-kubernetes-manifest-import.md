# ADR-20261029093621: Kubernetes manifests deploy through the project scan, and every field is applied or explained

- **Status:** proposed
- **Date:** 2026-10-29
- **Amends:** the Kubernetes row of `docs/repo_decomposition_implementation.md` §3
  (`Deployment`→server, `CronJob`→job, `StatefulSet`→refused, everything else
  skipped silently).
- **Extends to Kubernetes:** ADR-1337 (unsupported keys are reported), ADR-20261023120000
  (declared resources set app settings), ADR-20261023085230 and ADR-20261026181020
  (opt-in environment import and service address rewrite), ADR-682/683/684
  (healthchecks gate readiness and drive recovery).
- **Checked against:** `docs/api-hosting-roadmap.md` scope ("does not initially
  optimize for … stateful disks attached to application VMs, a
  Kubernetes-compatible surface"), ADR-20261026145832 (CLI command tree), the
  stateless contract (`docs/storage.md`) and ADR-20261023115741 (every capability
  behind a platform flag).

## Context

Teams leaving Kubernetes have their service described in manifests: the image,
command, ports, environment, sizes, probes, autoscaling, schedules and how
services call each other. They should not have to describe it a second time to
deploy to Gregale.

The project scan already reads Kubernetes manifests (`pkg/reposcan/k8s.go`) from
`k8s/`, `kubernetes/`, `deploy/` and `manifests/`. It is detector three of seven,
behind Compose and Procfile. What it reads is a fraction of what Compose gets:

- **Workloads.** It reads only the first container of a `Deployment` (as an HTTP
  app) and of a `CronJob` (as a job with its schedule), and refuses
  `StatefulSet`. Every other kind is skipped silently: `Service`, `Ingress`,
  `ConfigMap`, `Secret`, `HorizontalPodAutoscaler`, `Job`, `DaemonSet`,
  `NetworkPolicy`, `PersistentVolumeClaim` and CRDs.
- **Fields.** Of the pod spec it reads `image`, `command` + `args` (joined),
  environment variable *names* and `containerPort`. `resources`, the probes,
  `replicas`, `initContainers`, extra containers, `volumes`, `securityContext`,
  `terminationGracePeriodSeconds` and `timeZone` are dropped without a word.
  ADR-1337 ended exactly that for Compose: a file asked for something, the
  platform did something else, and nobody was told.
- **Fragility.** A document that does not decode into the manifest struct fails
  the whole scan (`reposcan: parse … document N`). `deploy/` is a common home
  for non-Kubernetes YAML: an Ansible playbook is a top-level list and does not
  decode (checked with `gopkg.in/yaml.v3`). So a repository with
  `deploy/site.yml` cannot be planned at all. Helm templates decode or fail
  depending on where their `{{ … }}` falls, so a chart under `deploy/` gives
  wrong workloads or an error.

The obvious design is a separate `gregale import k8s` command that writes
`gregale.yaml`. It is rejected below. A new top-level verb breaks the CLI tree,
and a second translator would drift from the scan that Compose, Procfile,
Render, Fly and app.yaml already share.

## Decision

Kubernetes manifests become a first-class project scan source, held to the same
rules as Compose. Translation is one-way and read-only: Gregale reads manifests
from the repository and never talks to a cluster, writes manifests, or serves
any Kubernetes API.

### 1. Where and how it runs

- **Same entry points.** `gregale deploy`, `gregale deploy --diff`, the dashboard
  and `gregale deploy --repo … --project` plan Kubernetes manifests as they plan
  Compose. There is no new command. The plan is the import report; nothing is
  written to the repository.
- **Same directories, plus one explicit setting.** The four root directories stay.
  A `gregale.yaml` may list other directories under `kubernetes.paths`, relative
  to the repository root and inside it, like every scan path. The new key is
  `omitempty`.
- **Only Kubernetes objects.** A document is read only when it has a string
  `apiVersion` and `kind`. Any other YAML document (an Ansible playbook, a values
  file, a GitHub workflow) is skipped. The file is named once in the scan
  warnings, and the scan does not fail. A document that has `apiVersion` and
  `kind` but is malformed still fails the scan with its file and document
  number, as today.
- **No rendering.** A directory holding `Chart.yaml`, or a `kustomization.yaml`,
  is skipped whole with one warning that gives the command to run:
  `helm template <release> <dir> > k8s/rendered.yaml` or
  `kubectl kustomize <dir> > k8s/rendered.yaml`. Gregale does not render charts
  or overlays.
- **Precedence is unchanged.** Compose still wins a tie on the same workload
  name. A repository with both reports the Kubernetes workloads it did not use,
  so nothing is applied twice.

### 2. Workload kinds

| Kind | Result |
|---|---|
| `Deployment` | An app. Request mode when a `Service` selects it, or when it declares a `containerPort`. Otherwise request mode with a warning: "no Service selects this Deployment; set `execution_mode: worker` if it consumes a queue". |
| `CronJob` | A job with its schedule, as today. `timeZone` sets the cron timezone and `suspend` sets enabled, as today. `concurrencyPolicy`, `startingDeadlineSeconds` and the history limits are reported. |
| `Job` | Reported: "one-off Jobs are run with `gregale job create` / `gregale job run`". |
| `StatefulSet` | Refused, as today. When its image is a known datastore, the plan offers the managed resource (`gregale add postgres`, …) through the existing denylist path. |
| `DaemonSet` | Refused: there is no per-node workload, and nodes are the platform's. |
| `PersistentVolumeClaim`, `PersistentVolume`, `StorageClass` | Reported with the stateless reason (`docs/storage.md`) and the managed alternatives. |
| `Service` | Not an app. Its selector links it to a Deployment (names, ports, address rewrite below). `type: LoadBalancer` and `NodePort` are reported: every app already gets an HTTPS URL. |
| `Ingress`, Gateway API `HTTPRoute` | Not applied. Each host becomes a plan note with the `gregale domain add` command for the app it routes to. Domains need ownership verification and are a public change, so the scan never adds one. Path fan-out across services is reported. |
| `HorizontalPodAutoscaler` | Section 4. |
| `ConfigMap` | A source of plain values for section 5. Mounted as a volume, it is reported: there are no file mounts. |
| `Secret` | Never imported (section 5). |
| `NetworkPolicy` | Reported. Private ranges and cloud metadata are always blocked, apps reach each other only through declared bindings, and `gregale app egress-allowlist` narrows outbound traffic. The policy is not translated, because an allowlist refuses every other destination, hostnames included. A mechanical translation would break the app in ways the policy author did not intend. |
| `ServiceAccount`, `Role`, `RoleBinding`, `ClusterRole*` | Reported, with scoped keys and organisation roles for people and CI. A cloud role annotation on a ServiceAccount (`eks.amazonaws.com/role-arn`, `iam.gke.io/gcp-service-account`, `azure.workload.identity/client-id`) adds a note pointing to [workload identity](../workload-identity.md) and the subject the trust policy must name (`app:<app_id>`). |
| `Namespace`, `PodDisruptionBudget`, `PriorityClass`, `LimitRange`, `ResourceQuota` | Ignored quietly: the platform owns placement and admission. A namespace is still used to resolve service addresses. |
| Anything else (CRDs included) | Generic warning naming the kind and file. |

### 3. Pod spec fields

The app's container is the one named by the
`kubectl.kubernetes.io/default-container` annotation, otherwise the first.

- **Process.** `command` becomes the entrypoint and `args` the command, the
  Kubernetes meaning, through the ADR-1337 process overrides. `workingDir`
  becomes `working_dir`; `securityContext.runAsUser`/`runAsGroup` become `user`.
  `terminationGracePeriodSeconds` becomes `stop_grace_period`.
- **Ports.** The port a selecting `Service`'s `targetPort` names, by number or by
  port name, is the serving port. Without a Service it is the first
  `containerPort`. Other ports are reported, with private TCP (ADR-576) for
  non-HTTP listeners.
- **Size.** `resources.limits.memory` sets `ram_mb` and `resources.limits.cpu`
  sets `cpu_millicores`, with ADR-20261023120000's rounding, plan checks and
  ownership. Without a limit, the request is used, with a warning: Gregale has
  one size, and it is both the guarantee and the cap. Kubernetes quantities
  (`Mi`, `Gi`, `M`, `G`, millicores `500m`) are parsed exactly. A size over the
  plan fails admission with the limit and the requested value, as the apps API
  does.
- **Health.**
  - `readinessProbe`, or `startupProbe` when there is no readiness probe (or
    only a `tcpSocket` one), becomes the app's healthcheck. The other probe's
    action is reported; its budget still counts toward the start period.
  - `exec.command` becomes an `api.ComposeHealthcheck` `CMD` test. It gates
    readiness (ADR-683) and replaces an instance that fails after it was ready
    (ADR-684).
  - `httpGet` and `grpc` become the deployment's startup probe
    (`api.DeploymentHealthcheck`): `httpGet.path` (default `/`) the HTTP path,
    `grpc.service` the gRPC health service. The scan stores it on the app
    (`project_readiness_check`), and every project deploy passes it as the
    deployment's healthcheck override, so it gates readiness on every boot, and
    as its liveness override with the plan's default timing, so an instance
    that fails it after it was ready is replaced, as ADR-684 does for an exec
    check. The liveness override is load-bearing: without it the plan-default
    liveness probe calls `/healthz`, and once readiness is an HTTP path a 404
    there counts as a failure, so an app that serves health elsewhere would
    restart in a loop. A plan without liveness overrides (Free, or gRPC where
    `GRPCLivenessAllowed` is false) gets neither, and its readiness stays the
    TCP listener.
  - The platform checks the serving port over plain HTTP or gRPC: a probe of
    another port, or with `scheme: HTTPS`, is reported and not applied, since it
    would check something else. A named port resolves through the container's
    `ports`. `host` and `httpHeaders` are reported. Two differences from the
    cluster are documented, not hidden: HTTP checks accept 2xx where Kubernetes
    accepts 200–399, and an instance that fails its readiness check after it was
    ready is replaced rather than taken out of traffic, which the scan reports.
  - `periodSeconds`, `timeoutSeconds`, `failureThreshold` and
    `initialDelaySeconds` map to interval, timeout, retries and start period,
    with the Kubernetes defaults (10 s, 1 s, 3, 0) when absent. A
    `startupProbe`'s `initialDelaySeconds + failureThreshold × periodSeconds`
    sets the start period.
  - `tcpSocket` is reported: the platform already waits for the serving port.
  - **`livenessProbe` is not imported.** It is reported with the reason: the
    healthcheck above already replaces a broken instance. A liveness probe that
    is stricter than readiness, or calls a dependency, is the usual cause of
    restart loops.
- **Other containers.** Sidecars are reported with a pointer to
  [companions](../companions.md). `initContainers` are reported. One whose name
  or command looks like a migration (`migrate`, `alembic`, `prisma`,
  `rails db:migrate`) adds a note naming the release command, and the plan does
  not set it.
- **Reported with a reason** (the Compose reasons where one exists):
  - `volumes`: `emptyDir` gets "write scratch data under /tmp"; the rest get the
    stateless reason.
  - `nodeSelector`, `affinity`, `tolerations`, `topologySpreadConstraints`: the
    scheduler places instances.
  - `hostNetwork`, `hostPID`, `privileged`, `capabilities`: kernel, device and
    namespace controls belong to the microVM.
  - `imagePullSecrets`: use `gregale registry` credentials.
  - `lifecycle`.

### 4. Replicas and autoscaling

Replicas follow ADR-20261023120000. A Deployment's `replicas`, or an HPA's
`maxReplicas` when an HPA targets it, becomes the app's instance cap
(`max_concurrency`). `minReplicas` is **not** imported. Instances start on
demand and park when idle, and the plan says how to keep N warm on purpose
(`scaling.min_instances`, billed while resident). An HPA CPU or memory
utilisation target is reported with the equivalent `scaling.targets` entry. It
is not applied, because the signal's meaning differs (Gregale targets per
instance, not per request).

### 5. Configuration and secrets

- **Opt-in.** A Deployment or CronJob annotated
  `gregale.dev/env-import: "true"` gets ADR-20261023085230's import. Plain
  literal `env[].value` entries count, and so do the plain values of `ConfigMap`
  keys reached through `env[].valueFrom.configMapKeyRef` or
  `envFrom[].configMapRef` among the scanned manifests. The same rules apply:
  credential-named keys and values the secret scanner flags are skipped, nothing
  overwrites a set value, the plan shows names only, and apply re-reads the
  reviewed source. Without the annotation, the plan names the importable keys
  and shows the one line to add.
- **Service addresses.** With the opt-in, a value that calls a scanned Service
  over HTTP is rewritten to the selected Deployment's Gregale address
  (ADR-20261026181020, DNS form per ADR-20261026170512). Accepted forms are
  `http://billing`, `billing.<ns>`, `billing.<ns>.svc` and
  `billing.<ns>.svc.cluster.local`, with any port. The plan names each rewrite,
  not the value.
- **Secrets are never imported.** `secretKeyRef`, `envFrom[].secretRef` and
  every key of a `Secret` manifest are listed under **Needs a value**, with
  `gregale env set` (sealed at rest) or `gregale deploy --secrets-file`. Their values never reach the
  plan, receipt, logs or audit events. A committed `Secret` manifest with
  `data` or `stringData` also raises a scan finding: base64 is not encryption,
  so the values are in the repository's history and should be rotated.

### 6. Edge behaviour declared on an Ingress

Common proxy annotations (`nginx.ingress.kubernetes.io/proxy-read-timeout`,
`proxy-body-size`, `proxy-send-timeout`, websocket and `ssl-redirect` settings,
and their Traefik and HAProxy equivalents) are not applied. Each becomes a plan
note comparing what the manifest asked for with what Gregale does on this plan.
Examples: "proxy-body-size 50m: this plan accepts request bodies up to 25 MB",
"proxy-read-timeout 120: requests time out after 30 s by default; a
`kind=budget` edge rule allows up to 5 m on this plan", "WebSockets: enable
with `gregale app <slug> --websocket-enabled`". The numbers come from
`pkg/api/limits.go` at plan time, never from the docs.

The scan records each recognised annotation on the workload its Ingress
routes to: through each backend Service (`spec.rules[].http.paths[].backend`,
`spec.defaultBackend`, and the `extensions/v1beta1` `serviceName`) to the one
scanned Deployment the Service selects in its namespace. A
`websocket-services` annotation covers only the Services it lists. A backend
that selects no scanned Deployment, or several, is reported. The plan step,
which knows the account's plan, formats the notes (`projectsetup.IngressNotes`).

| Setting | Annotations | Note compares with |
|---|---|---|
| Request body size | `nginx.ingress.kubernetes.io/proxy-body-size`, `nginx.org/client-max-body-size`, `haproxy-ingress.github.io/proxy-body-size` | the plan's `RequestBodyMaxBytes` |
| Read / send timeout | `nginx.ingress.kubernetes.io/proxy-{read,send}-timeout`, `nginx.org/proxy-{read,send}-timeout`, `haproxy.org/timeout-server`, `haproxy-ingress.github.io/timeout-server` | the app request budget default and the ceiling `--request-timeout` may reach on the plan (ADR-093, ADR-1150) |
| WebSockets | `nginx.ingress.kubernetes.io/websocket-services`, `nginx.org/websocket-services` | whether the plan forwards WebSockets by default, allows opting in, or refuses |
| HTTPS redirect | `nginx.ingress.kubernetes.io/{ssl,force-ssl}-redirect`, `nginx.org/redirect-to-https`, `haproxy.org/ssl-redirect`, `haproxy-ingress.github.io/ssl-redirect`, `ingress.kubernetes.io/ssl-redirect`, `traefik.ingress.kubernetes.io/redirect-entry-point` | TLS ends at the Gregale edge |

Traefik sets body size and timeouts through Middleware objects, not
annotations, so only its redirect annotation is recognised. Other Ingress
annotations (rewrites, CORS, rate limits) are not compared; the Ingress kind
warning still names its hosts for `gregale domain add`.

### 7. Release, gating and limits

- **Capability.** The new mappings ship behind a `kubernetes-manifests` preview
  capability (ADR-20261023115741). With it off, the scan keeps today's mapping,
  but sections 1 (skip non-Kubernetes YAML, no rendering) and 2's reporting
  apply to everyone: they turn silent drops and spurious failures into warnings.
- **Bounds.** The number of manifest files, documents per file and bytes read
  are bounded by constants in `pkg/api/limits.go`
  (`KubernetesScanMaxFiles` 500 YAML files, `KubernetesScanMaxDocuments` 1,000
  documents in one file, `KubernetesScanMaxBytes` 16 MiB across every file),
  with the existing safe-path reader. They bound every scan, with or without
  the capability. Exceeding one fails the scan with the limit and the observed
  value.
- **Version skew.** New plan and receipt fields are additive and `omitempty`. An
  older apid plans a repository with today's mapping, and a newer CLI shows only
  the fields it receives. No migration and no proto change.
- **Price.** Free on every plan: it is a software feature, and the resources it
  creates are billed as before.

## Roadmap non-goals check

| Roadmap limit or rule | This ADR |
|---|---|
| **No Kubernetes-compatible surface** | Kept. Manifests are one more input to the existing project scan, like `fly.toml` and `render.yaml`. There is no Kubernetes API, no `kubectl` support, no CRDs, no cluster connection and no write-back. A team keeps or deletes its manifests as it likes; Gregale behaviour comes from the plan it reviewed. |
| **No stateful disks attached to application VMs** | Kept. StatefulSets, PVCs and non-`emptyDir` volumes are refused or reported, and datastores are offered as managed resources. |
| Static-site hosting, every-language buildpacks | Untouched. |
| Target workload: "existing HTTP API … without rewriting it" | Served directly: a team on Kubernetes has its API's run contract in manifests. |
| CLI tree: "No other verb is added at the top level" | Kept: no `import` verb. The existing `deploy` / `deploy --diff` plan does the job. |
| Builds in builder VMs (ADR-003) | Kept. The scan reads YAML only and renders nothing. Helm and Kustomize run in the customer's own environment. |
| Secrets sealed, never logged | Kept and extended: Secret manifests are never imported, and committed ones are flagged. |

## Why

- **The migration audience is real, and the scan is most of the way there.** Compose
  projects reached parity through ADR-1337, ADR-20261023120000 and the env-import
  ADRs. Kubernetes projects still lose their sizes, probes and configuration
  without a signal, and a stray Ansible file blocks them entirely.
- **Safe defaults beat literal translation.** The mappings deliberately drop the
  Kubernetes settings that cause the classic incidents:
  - aggressive liveness probes (restart loops);
  - `minReplicas` (always-on spend on a scale-to-zero platform);
  - translated NetworkPolicies (outbound traffic broken in ways the author did
    not intend);
  - committed Secrets.

  Each one is explained, so nothing is lost silently.
- **The plan explains the platform's defaults.** The Ingress notes put the
  limits that usually surprise people (timeout, body size, WebSockets) into the
  review, before the first request fails.

## Consequences

- A Kubernetes repository plans with its images, processes, ports, sizes,
  healthchecks, schedules, instance caps and, opted in, plain configuration and
  service wiring. What does not apply is listed with the Gregale way to do it.
- Repositories with non-Kubernetes YAML under `deploy/` (Ansible, Helm charts)
  that fail today will plan. Their warnings list the skipped files.
- `minReplicas` and liveness probes are not honoured. Teams that relied on
  always-on pods see first-request wakes until they set `scaling.min_instances`.
  The plan says this per app.
- `command` and `args` are split into entrypoint and command instead of joined
  into one command. Where the two meanings differ (an image `ENTRYPOINT` with a
  manifest `command`), the frozen process changes. The plan shows it, and the
  change is behind the capability. The implementation PR pins today's behaviour
  in a test before changing it.
- Docs: `docs/multi-service-apps.md` "What becomes a service" gains the
  Kubernetes source, and a new `docs/kubernetes.md` maps each Kubernetes concept
  to its Gregale counterpart. Customer docs wait for the capability's GA, per
  the "gate customer features until prod-tested" rule.

## Rejected alternatives

- **`gregale import k8s` writing `gregale.yaml`.** It breaks ADR-20261026145832
  rule 5 and would be a second translator beside `pkg/reposcan`, drifting from
  Compose's rules. A one-time conversion also leaves two sources of truth on day
  two. The plan already shows the result; if a "write gregale.yaml from the plan"
  export is wanted later, it belongs to the scan for every source, not to
  Kubernetes.
- **Rendering Helm and Kustomize in the scan.** Rendering takes values from
  outside the repository (`--set`, values files per environment, remote bases,
  chart repositories over the network). The rendered plan would not be
  reproducible from the reviewed source, and the plan token binds the source
  hash. Rendering in a builder VM is possible later, as its own decision.
- **Translate NetworkPolicy into the egress allowlist.** Kubernetes policies
  select peers by label and namespace, and allow DNS and in-cluster traffic
  implicitly. An allowlist is CIDR-based and refuses hostnames not on it. A
  faithful mapping does not exist, and an unfaithful one fails closed in
  production.
- **Import `minReplicas` as warm instances.** It would quietly turn a
  scale-to-zero app into a resident, billed one. It is better to say so and let
  the team opt in.
- **Import liveness probes as a restart trigger.** It would carry over the
  probe configuration most often responsible for restart loops. The readiness
  healthcheck already recovers broken instances.
- **Read the cluster (`kubectl get -o yaml`) instead of the repository.** It
  needs cluster credentials, picks up controller-written fields and drift, and is
  a step toward the Kubernetes surface the roadmap rules out.
