# Environment variables and secrets

Set values before the first request; the dry run lists keys the code reads that will have no value.

- Set an environment variable:
    gregale env set --app <app> KEY=<value>
- Push a local .env file:
    gregale env push --app <app> -f .env
- Set a secret (sealed, never shown again):
    gregale secrets set --app <app> KEY=<value>
- List an app's secret names:
    gregale secrets list --app <app>
- Set one value for every service of a project environment:
    gregale environment secrets set <project> <environment> KEY=<value>

Notes:
- Never write secret values into source, Dockerfiles or gregale.yaml.
