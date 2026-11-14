# Environment variables and secrets

Set values before the first request; the dry run lists keys the code reads that will have no value.

- Set a value (stored sealed; never shown again):
    gregale env set --app <app> KEY=<value>
- Push a local .env file:
    gregale env push --app <app> -f .env
- List an app's key names:
    gregale env list --app <app>
- Set one value for every service of a project environment:
    gregale environment secrets set <project> <environment> KEY=<value>

Notes:
- Never write secret values into source, Dockerfiles or gregale.yaml.
