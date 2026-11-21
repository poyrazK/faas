# Versioned Run tasks and durable Agent continuations

Status: implemented locally; functional KVM qualification passed; new admissions default off.

Run tasks are account-owned, immutable named versions of a bounded source Run.
Account administrators publish code, runtime/profile or environment, input/result
schemas, resource limits, output declarations and managed integration IDs once.
Runs-only credentials may discover metadata and invoke a version; their inputs,
receipts and files retain existing API-key-family isolation. Publishing and
retirement require account-wide write authority. Reads never return source or
encrypted definition material. There are no mutable aliases in this increment.

Definitions are sealed to the Account domain, whose reader is apid. Invocation
opens the definition there and seals a fresh request/input to Delivery for schedd.
Task names and version numbers cannot be reused with different content. Retirement
erases retained code, blocks new admissions and preserves immutable metadata.
Already-admitted Runs own complete sealed payloads and continue independently.
Catalog metadata, encrypted bytes and invocation envelopes have central limits.
Retired version metadata continues to count toward the bounded preview catalog.

The existing atomic execution admission transaction also writes a task invocation
sidecar. The physical executions table layout stays unchanged for N-1 readers.
Version retirement, plan quota checks, input/grant validation, Run payload and
invocation identity commit together. Replays precede definition opening, writer
gates, dependency readiness and quota checks. A task's fixed environment grants
access only to that published environment; dynamic input files retain the
invoker's existing scope. Integration policies are rechecked at ordinary Run
admission and at each outbound call.

For stateless Agents, add the managed RAP tool `gregale_task`, using the existing
authenticated caller, signed callback grant and owned thread inbox. A version
must explicitly allow the endpoint before its workload may invoke it. The tool
accepts JSON input, never raw code, credentials or arbitrary callback targets.
Its endpoint/thread/tool-call identity is bound to the Run in the admission
transaction and replayed durably, including after the ordinary admission key
window. Request fingerprints fail closed across unavailable key epochs.

Finished task invocations are a durable completion inbox: their committed Run
terminal state makes the linked continuation eligible. apid publishes a stable
RAP tool-result message through the existing idempotent realtime inbox before
marking the link delivered. A crash after publication repeats the same message
identity. There is one logical continuation record; delivery/processing remains
at least once and external effects need idempotency. This starts the next slice
of the customer's agent; Gregale adds no agent loop or persistent guest session.

Results and artifacts are fetched through a task-specific read boundary that
checks the invocation's endpoint against the authenticated app/account. Ordinary
Run reads keep their original key-family boundary. Oversized inbox results use
receipt references. Failed, cancelled, timed-out, OOM and invalid result contracts
are explicit tool outcomes. Thread revocation or endpoint removal retires the
continuation; temporarily disabling new task or Agent admissions preserves pending
delivery obligations. A version's retirement does not revoke admitted work.

Apply additive tables and upgrade readers/workers before enabling
`FAAS_EXECUTION_TASKS_ENABLED` together with the existing Run API/V2 gates. Agent
tools additionally require the Agents capability. Disabling task writers retains
catalog/receipt reads and completion delivery. No VM lifecycle or recovery owner
changes; schedd/vmmd retain execution ownership. Native production release gates
and hosted Agent harness policy remain unchanged.

Acceptance requires immutable publication, scope and endpoint fences, concurrent
and lost-acknowledgement invocation replay, retirement/writer rollback, quota and
transaction rollback, duplicate completion publication and agent-worker restart.
An actual report task must execute in a disposable VM and hand its structured
result and artifact to a fresh stateless continuation with one Run and one inbox
record. Initial/final physical leakchecks remain required for that functional lane.

The [functional report](../ops/evidence/20261115-run-tasks/README.md) records the task/continuation VM journey. Native production runtime eligibility and rollout remain separate gates.
