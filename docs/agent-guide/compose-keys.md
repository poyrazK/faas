# Gregale keys in a Compose file

Settings a Compose file carries for Gregale. Standard keys set RAM, CPU, instances and migrations; x-gregale-* keys set the rest. Other Compose tools ignore them.

- Preview what each service gets (memory=, release:, visibility):
    gregale deploy --project --dry-run
- See which file sets a project setting:
    gregale config explain services.<service>.visibility
- Write a deployed project back as Compose with its x-gregale-* keys:
    gregale project export <project> --out compose.yaml
- Change RAM after a deploy (the file's memory wins on the next deploy):
    gregale app <app> --ram <MB>

Notes:
- RAM: deploy.resources.limits.memory: 512M (or mem_limit: 512m) on the service. Without it a new service starts with 128 MB. CPU: deploy.resources.limits.cpus (0.25, 0.5 or 1). Instances: deploy.replicas.
- Migration: a service the others wait for with depends_on: {migrate: {condition: service_completed_successfully}} runs as the release command of a waiting web service or worker built from the same build:/image:. Or put release: in a Procfile (or release.command in gregale.yaml) in the service's directory. There is no x-gregale release key.
- Top level: x-gregale-slug-prefix: <prefix> (new apps <prefix><service>); x-gregale-env-import: true (import plain environment: values); x-gregale-public-auth and x-gregale-service-policy (defaults for every service).
- Per service, access: x-gregale-access: public|key; x-gregale-public-auth: open|bearer; x-gregale-allow-callers: [svc]; x-gregale-allow-call-scopes: {svc: {methods, path_prefixes}}; x-gregale-preview-calls: allow|deny; x-gregale-platform-tenant-required: true.
- Per service, calls: x-gregale-service-policy: project|declared|account; x-gregale-service-transport: http|https; x-gregale-service-reliability: {dep: {timeout_ms, max_attempts}}.
- Per service, runtime: x-gregale-app-protocol: http1|http2|grpc; x-gregale-startup-deadline: 90s (max 120 s); x-gregale-streaming: true; x-gregale-wake-ahead: true; x-gregale-renamed-from: <old service name>.
- Full table: https://gregale.dev/docs/container-compatibility#compose-key-reference
