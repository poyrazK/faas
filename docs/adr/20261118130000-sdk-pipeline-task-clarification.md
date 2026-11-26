# ADR-20261118130000 SDK pipeline Task clarification

The Node and Python Task views project an authorized pending external-input
gate as a form elicitation request when the client advertises form support.
Applications register the reply contract, gate service, prompt, and response
scopes for each pipeline tool. Clients with basic Task support can follow the
authenticated review URL. Approvals continue through the authenticated review
flow.

The same Task owner key signs a request key bound to the Task, accepted
receipt, run, gate ID, and gate binding. A poll after reconnect can regenerate
that key without storing a separate form. A reply requires current Task and
gate authorization and the original binding. The gate service validates the
typed answer and uses its existing first-decision-wins record and idempotent
wake delivery. The same authorized actor can retry the recorded answer
after the run advances.
