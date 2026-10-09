# approval-flow

Implement POST /request-approval, /fulfill-order, and /expire-approval. Store the run ID received in X-Faas-Workflow-Run-Id with the approval request so your authorized service can deliver order.approved to that exact run. For example: gregale workflows events RUN_ID order.approved --payload '{"approved_by":"reviewer-42"}'. Restrict that operation to authorized approvers. The included attempt mock times out the event wait, exercises /expire-approval, and skips /fulfill-order. The alternate approval mocks resume the wait with an approved_by payload, exercise /fulfill-order, and skip /expire-approval. Verify real event delivery and authorization with an authenticated event on a test run.

## Preview

Fulfillment requires an approved_by field in the approval event. From this directory, replace APP_SLUG with your app:

```sh
gregale automations validate --app APP_SLUG --file automation.yaml
gregale automations simulate --app APP_SLUG --file automation.yaml \
  --input-file sample-input.json --mock-outputs-file mock-outputs.json \
  --mock-attempts-file mock-attempts.json --require-complete
```

Preview successful approval using the alternate sample files:

```sh
gregale automations simulate --app APP_SLUG --file automation.yaml \
  --input-file sample-input.json \
  --mock-outputs-file mock-approval-outputs.json \
  --mock-attempts-file mock-approval-attempts.json --require-complete
```

The trace includes the supplied approval payload and event_received_mocked reason. This checks mappings and branching; it does not prove that an event can be delivered to a live run.

Simulation validates control flow and sample mappings without calling handlers or providers. Review the complete trace, including the timeout route for approval-flow. Real handlers need idempotent side effects because execution can retry. These mocks do not prove that your handlers, integration permissions, or external services are ready.

## Check scenarios

Run all supplied scenarios and assertions together:

```sh
gregale automations check --app APP_SLUG --scenarios scenarios.yaml
```

Checks require complete traces by default and return exit code 1 for an assertion, validation, or simulation failure. Use the global --json option for a structured report. This command uses simulation; it does not start real runs.

## Publish

Deploy the required handlers first. After editing and validating the definition, save a draft with `gregale automations apply --app APP_SLUG --file automation.yaml --expected-version 0` for a new name. For an existing name, use its current version. Publish with `gregale automations publish --app APP_SLUG --name approval-flow --expected-version VERSION --scenarios scenarios.yaml`, using the version returned by apply. This checks the saved draft before publishing and blocks publication if a scenario fails. Publishing can enable automatic starts for an event automation. Review trigger settings before publishing.

Docs: https://gregale.dev/docs/automations
