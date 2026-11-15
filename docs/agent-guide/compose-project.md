# Deploy a Compose project

Each Compose service becomes an app in one project. Gregale does not run Docker's network: service names do not resolve.

- Preview every service, its URL, health check and managed databases:
    gregale deploy --project --dry-run
- Deploy the project:
    gregale deploy --project --yes
- Deploy some services only:
    gregale deploy --project --only <service>
- Open a public service to everyone:
    gregale app update <app> --no-require-authn
- Follow every service's logs:
    gregale logs --project <project> --follow
- Run a command in a fresh instance of a service:
    gregale app exec <app> -- <command>

Notes:
- Plain environment: values are imported only with `x-gregale-env-import: true` at the top of the Compose file; a URL that names a sibling (http://api:8080) is rewritten to its Gregale address without it. Do not copy them into Dockerfiles.
- A gRPC service declares `x-gregale-app-protocol: grpc` on its Compose service.
- Too many files: each service's .dockerignore applies inside its build context; list directories no service builds from (k8s, docs) in .gregaleignore.
- Code that dials a Compose name (redis, db) must read $GREGALE_SERVICE_<NAME>_HOST (raw TCP) or $GREGALE_SERVICE_<NAME>_URL (HTTP); the caller lists the service in depends_on. The dry run names each place.
- A postgres service becomes a managed database bound as DATABASE_URL. Gregale does not run other datastores (redis, mysql, mongo): an app parks to a snapshot and keeps no data. Point the code at a hosted one (Upstash, PlanetScale, Atlas) with an env URL.
- A build target or command for development (target: dev, nodemon, --reload) is deployed as written.
