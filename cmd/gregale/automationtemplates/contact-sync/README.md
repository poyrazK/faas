# contact-sync

Replace the example integration UUID in automation.yaml with your customer managed integration ID. Bind that integration to the app and allow GET /v1/contacts in both integration and app route policies. Implement POST /sync-contact. Runtime requires managed workflow outbound execution to be enabled. Credentials belong in the managed integration, never in these files. This starter fetches a provider contact and passes it to your app handler; it does not modify the provider contact.

## Preview

Complete the integration setup above first: server validation checks real app bindings even when provider responses are mocked. From this directory, replace APP_SLUG with your app:

```sh
gregale automations validate --app APP_SLUG --file automation.yaml
gregale automations simulate --app APP_SLUG --file automation.yaml \
  --input-file sample-input.json --mock-outputs-file mock-outputs.json \
  --mock-attempts-file mock-attempts.json --require-complete
```

Simulation validates control flow and sample mappings without calling handlers or providers. Review the complete trace before publishing. Real handlers need idempotent side effects because execution can retry. These mocks do not prove that your handlers, integration permissions, or external services are ready.

## Check scenarios

Run all supplied scenarios and assertions together:

```sh
gregale automations check --app APP_SLUG --scenarios scenarios.yaml
```

Checks require complete traces by default and return exit code 1 for an assertion, validation, or simulation failure. Use the global --json option for a structured report. This command uses simulation; it does not start real runs.

## Publish

Deploy the required handlers first. After editing and validating the definition, save a draft with `gregale automations apply --app APP_SLUG --file automation.yaml --expected-version 0` for a new name. For an existing name, use its current version. Publish with `gregale automations publish --app APP_SLUG --name contact-sync --expected-version VERSION --scenarios scenarios.yaml`, using the version returned by apply. This checks the saved draft before publishing and blocks publication if a scenario fails. Publishing can enable automatic starts for an event automation. Review trigger settings before publishing.

Docs: https://gregale.dev/docs/automations
