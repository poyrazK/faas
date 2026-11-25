# Deploy a Compose project

Each Compose service becomes an app in one project. Gregale does not run Docker's network: service names do not resolve.

- Preview every service, its URL, health check and managed databases:
    gregale deploy --project --dry-run
- Deploy the project:
    gregale deploy --project --yes
- Deploy some services only:
    gregale deploy --project --only <service>
- Restart, health-check or change every app of the project at once:
    gregale app restart --project <project> --fresh --wait
- Follow every service's logs:
    gregale logs --project <project> --follow
- Run a command in a fresh instance of a service:
    gregale app exec <app> -- <command>

Notes:
- Plain environment: values are imported only with `x-gregale-env-import: true` at the top of the Compose file; a URL that names a sibling (http://api:8080) is rewritten to its Gregale address without it. Do not copy them into Dockerfiles.
- A gRPC service declares `x-gregale-app-protocol: grpc` on its Compose service.
- A browser-facing service declares `x-gregale-access: public` on its Compose service (or `access: public` under project.services.<name> in gregale.yaml): its URL needs no Gregale API key, and every production deploy keeps it so. Removing the key makes the URL need a key again. Use this instead of `gregale app update <app> --no-require-authn`, which the next deploy undoes for a service that declares access.
- Too many files: each service's .dockerignore applies inside its build context; list directories no service builds from (k8s, docs) in .gregaleignore.
- Code that dials a Compose name (redis, db) must read $GREGALE_SERVICE_<NAME>_HOST (raw TCP) or $GREGALE_SERVICE_<NAME>_URL (HTTP); the caller lists the service in depends_on. The dry run names each place.
- A postgres service becomes a managed database bound as DATABASE_URL, and a redis service a managed Redis cache bound as REDIS_URL where managed Redis is on for the account (`gregale feature list`). Gregale does not run other datastores (mysql, mongo): an app parks to a snapshot and keeps no data. Point the code at a hosted one (PlanetScale, Atlas) with an env URL.
- A build target or command for development (target: dev, nodemon, --reload) is deployed as written.
- A gRPC address (PRODUCT_SERVICE_ADDR: products:3550) is rewritten to products.svc.gregale:10081. Set x-gregale-streaming and x-gregale-wake-ahead on a service instead of later app commands.
- Every x-gregale-* key, RAM and migrations in Compose: see `gregale guide compose-keys`.
