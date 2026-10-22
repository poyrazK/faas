# Durable entity development

Start with the Node `durable-entity-node` template. Its [workflow guide](../cmd/gregale/templates/durable-entity-node/README.md)
covers SDK packaging, installation, checked handler registration, local invocation,
replay and fault injection, virtual-clock alarms, checkpoints, validator packaging
and deployment source checks. Counter, reservation and automation-session examples
ship together and share pure state decoders with the isolated restore validator.

Use `@gregale/sdk-node/durable-entities/runtime` for `defineDurableEntity` and
`createDurableEntityRegistry`. Definitions bind a namespace, schema, payload decoder
and synchronous transition function. Schema migrations remain opt-in and are only
persisted by a successful transition. Candidate validation does not migrate state.

Use `@gregale/sdk-node/durable-entities/testing` for `DurableEntitySimulator`.
This separate entrypoint supplies per-key serialization, receipts, a manual clock,
commit fault injection and local checkpoints without account or bucket credentials.
The platform remains responsible for object-storage durability and fenced ownership.

Projects opt in to mandatory artifact checks through `gregale.entities.json`.
Ordinary projects retain their existing deployment behavior. `gregale doctor` exposes
the same checks; deploying a declared project cannot bypass them with `--no-doctor`.
These checks do not execute JavaScript or establish production admission.

This is the first complete starter workflow for Node. Existing Go and Python entity
clients and wire helpers continue to use their existing APIs. Production feature
gates remain off by default, and native KVM/GCS acceptance is still required.

Source and behavioral CI coverage accompany this workflow. Local builds and tests
were deliberately not run during implementation; infrastructure qualification is
owned by the acceptance agent.

For deployed metadata diagnostics and a bounded changed-only watch, see
[durable entity visibility](durable-entity-visibility.md).
