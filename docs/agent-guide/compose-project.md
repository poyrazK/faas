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

Notes:
- Code that dials a Compose name (redis, db) must read $GREGALE_SERVICE_<NAME>_HOST (raw TCP) or $GREGALE_SERVICE_<NAME>_URL (HTTP); the caller lists the service in depends_on. The dry run names each place.
- A postgres service becomes a managed database bound as DATABASE_URL. Other datastore images (redis, mongo) are not run: point the code at one you run elsewhere, or run it as an app (`gregale guide private-services`).
- A build target or command for development (target: dev, nodemon, --reload) is deployed as written.
