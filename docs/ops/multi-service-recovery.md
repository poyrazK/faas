# Multi-service projects: diagnosis and recovery

Projects, internal services, service bindings and service wake-ahead
(ADR-20261027120000). Owner: apid maintainers (projects, bindings) and gateway
maintainers (service proxy, wake-ahead). The customer guide is
[multi-service apps](../multi-service-apps.md).

A call between services goes from the guest, through the node resolver
(`<service>.svc.gregale` and `<service>.internal` answer the tenant bridge),
to gatewayd-internal's service proxy. The proxy resolves the name, authorizes
the caller from one committed Postgres view, wakes a parked target, and
forwards over vmmd's bridge. Every call is counted in
`gateway_service_call_total{outcome}`.

## Triage by outcome

| Outcome / response | Meaning | Check |
|---|---|---|
| `not_found`, 404 `service is not registered` | No project service or same-account app has that name. | The caller uses the DNS form of the name: a Compose `billing_api` is `billing-api` (ADR-20261026170512). The two apps are in the same project (`gregale project get <project>`); `gregale project delete` detaches them. |
| `binding_denied`, 403 `caller has not declared this service binding` | The caller runs the `declared` policy and did not list the target. | `gregale app binding service explain <caller> <target>`. Add the target to the caller's Compose `depends_on` and redeploy. A gatewayd-internal older than #5651 refuses every declared call to a project service; upgrade the node. |
| `binding_denied`, 403 `caller has no active GitOps binding for this service` | The project published a release set and the caller's frozen graph has no binding to that name. | `gregale environment release-sets …` for the active set. A gatewayd-internal older than #5676 refuses names with `_` or `.`; upgrade the node. |
| `caller_denied`, 403 `target does not allow this service caller` | The target's allowlist does not name the caller. | The target's `allowed_service_callers` (`gregale app binding service explain <caller> <target>`). Entries are service names in DNS form or app slugs. |
| `scope_denied`, 403 `target service does not allow this caller method and path` | The target's call scope for this caller excludes the method or path. | The target's `allowed_service_call_scopes`. |
| `preview_denied`, 403 `production service does not accept calls from preview apps` / `preview_production_dependency_denied` | A PR preview called a production service its project does not open to previews. | The project's preview service policy ([preview environments](../preview-environments.md)). |
| `wake_failed`, 503 `service could not be woken` | Admission or restore failed for a parked target. | gatewayd-internal logs `service proxy wake failed app=<id> err=…`. Then `gregale app wake-timeline <target> <wake-id>`. The target needs a live deployment with a snapshot or a cold-bootable rootfs. |
| `wake_queue_full` | The node's wake queue is at its cap. | Node residency and wake latency dashboards; see [operator cold-boot recovery](operator-cold-boot-recovery.md). |
| 503 `service policy snapshot is unavailable` | gatewayd-internal could not read the policy view from Postgres within `TrafficServicePolicyReadTimeout`. | Postgres health and gatewayd-internal's pool. The response carries `Retry-After: 1`, and callers retry. |
| Guest DNS fails for `*.svc.gregale` | The node resolver on the tenant bridge is not answering. | gatewayd-internal's service-discovery DNS listener on the node. External names forward to the configured upstream resolvers. |

## Project apply problems

- **A plan fails with `depends on unknown service`.** A `depends_on` entry
  names a service that is not in the repository. Names are the Compose service
  names, as the plan lists them.
- **A dependent gets no `GREGALE_SERVICE_*` URL.** The dependency is not in
  the project, or the dependent excluded it with `--exclude`. Run
  `gregale project get <project>` to see what each service calls.
- **A service disappeared after a deploy.** It was removed from or renamed in
  Compose without `x-gregale-renamed-from`. The plan listed the removal. The old
  app is soft-deleted; adding the service back under the same name and
  deploying restores that app while its row still exists.
- **A rename failed with a conflict.** The new name belonged to a service
  deleted earlier, and the project still holds that member
  (ADR-20261029090000). Restore and remove it, or choose another name.

## Wake-ahead

Wake-ahead is on by default for project services under the `project` policy
(ADR-20261025105812). A caller's first cold wake restores the services it
declares in `depends_on`; later wakes also use what the gateways learned. It
is skipped while fleet residency is high. Turn it off per app with
`gregale app wake-ahead <app> off`. `gateway_service_wake_ahead_total` and the
other `gateway_service_wake_ahead_*` metrics show predictions, wakes and
skips.

## Switching a capability off

Each capability has a platform flag ([platform flags](platform-flags.md)):

```sh
gregalectl flags disable --capability projects --reason "incident <n>" --yes
gregalectl flags deny --capability service-wake-ahead --account <account-uuid> --reason "<why>" --yes
```

These flags gate apid only; none of the four capabilities has a daemon
consumer. Turning `projects` off hides `/v1/projects`, so new project deploys
are refused. Turning `service-bindings` or `service-wake-ahead` off refuses
their settings and hides their routes and commands. Deployed services keep
running, calling each other and waking ahead, because the service proxy does
not read the flags. To stop wake-ahead for an app, turn it off per app as
above. To undo a gatewayd-internal change, roll the release back in the normal
order: data → control plane → edge/node → runtime.
