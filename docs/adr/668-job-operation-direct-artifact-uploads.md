# ADR-668: Direct private artifact uploads for Job Operations

Status: implemented and locally qualified in closed admission; native qualification pending.

## Context

ADR-665 retains verified copies of customer-managed objects. The Job export
starter still needs an app upload endpoint, private bucket and storage credential
to produce that source. Ordinary Job handlers should be able to return a private
file using their existing scheduler capability.

## Decision

Add tokenless Job runtime binary upload and JSON receipt-lookup routes. A stable
report ID, name, exact size and SHA-256 describe a file. Gregale derives an opaque
`operation://<operation UUID>/artifacts/<artifact UUID>` reference; the request
contains no source URI, provider URL, bucket or storage key. The shared native
task authority and artifact ledger retain their existing fencing and quotas.
Managed-source preparation remains supported; HTTP and workflow source attachment
continue to require managed object references.

Each attempted copy reserves a fresh durable staging object before I/O. Receive
bytes under the shared account/node/spool budget and transfer deadline, verify
exact size/digest, recheck authority, write the unique private storage object,
then commit its receipt under the current native claim. Concurrent matching
copies converge on one receipt. Losers and failed/partial transfers keep durable
cleanup intents; they cannot overwrite the winner. Changed report declarations
conflict. A committed replay may return without reading the upload body.

The SDK snapshots bounded text/bytes once and coalesces matching calls. It
looks up a durable receipt before each transport retry, preserving the original
declaration and bytes. Private platform file transport may retry; business code
never retries automatically. The SDK memory default is independent of the
captured plan quota, and applications can supply a smaller or larger bound.

Only host-confirmed task success with typed output, or explicit account success
recovery, publishes files. Cancellation/replaced claims prevent new preparation.
Uncertain task outcomes retain private copies for inspection. Approved recovery
uses a fresh generation/run and fresh receipts. Native execution and notification
outcomes remain separate. Existing retention, blob cleanup and account deletion
semantics apply; no new database schema or VM lifecycle policy is needed.

## Validation and rollout

Qualify both stores and the HTTP boundary for size/hash/metadata rejection,
scope/claim fencing during I/O, duplicate/lost replies, unique staging keys,
private publication, recovery and cleanup. Update API/generated SDK contracts,
exercise the packed SDK starter without a bucket or upload broker, and add native
guest direct-upload evidence. Portable/compile checks do not qualify actual KVM
execution, provider failure behavior or fleet rollout. Production admission stays
closed until those activation gates pass.

The Node export starter now uses direct uploads and removes its app broker,
managed bucket setup and storage credential. Go has streaming upload and receipt
clients; generated Node/Python contracts expose the routes and descriptor.
The fifth scenario in [native qualification](../ops/customer-job-operations-native.md)
exercises direct bytes, private publication, lost acknowledgements and native
proof rejection. Its actual KVM execution is still pending.
