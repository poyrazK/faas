# ADR-1351 · Durable rehearsal recorder sessions

- **Status:** accepted; enrollment remains disabled pending native qualification
- **Date:** 2026-10-17
- **Decision:** Require a durable recorder for every simulation serving-VM execution, opened before boot and bound to the account, app, run, instance and deployment. Persist a request obligation before contacting outboundd. Require the owning vmmd to acknowledge a closing barrier before sealing complete evidence.
- **Why:** A failed broker connection or database write before simulator admission can otherwise leave an empty passing action inventory.
- **Consequences:** Missing recorders, lost owners and unacknowledged requests cannot produce complete evidence. The host parses supported relay HTTP requests and gives each its own broker connection and immutable request identity. The simulator atomically admits and completes that obligation with the attempt ledger. No provider fallback exists.
- **Rejected alternatives:** Inferring completion from inactivity, reconstructing a crashed recorder from database rows, treating a broker error as no action, or closing a recorder while its HTTP parser or handlers remain active.

## Protocol and authority

Simulation registration acknowledges `recorder_version: 1`; the Go client
requires it before configuration or deployment. Webhook-only runs keep version
1 behavior. Version-2 snapshots carry recorder version, session count, pending
session count and incomplete session count. Old simulation snapshots without
this acknowledgement cannot establish the new coverage.

vmmd opens a fresh, non-adoptable session before a supported serving VM boots.
Postgres verifies the scheduler-owned instance and deployment identity against
the immutable retained run policy. The owner holds its session across resident
pause/resume. Failed boot, unexpected process exit or loss of the owner leaves
failed or unacknowledged evidence; a replacement instance has a new session and
cannot close its predecessor. Expected retirement before closing is currently
conservative and must not establish a successful rehearsal by itself.

Each parsed relay request receives an obligation before broker dialing. Its
identifier travels only in the host-authenticated relay preamble. outboundd
binds it to verified account/app/run/instance identity and atomically associates
exactly one simulation attempt. Successful or deliberately injected fixture
outcomes complete the obligation in the same transaction as attempt completion;
unsupported or interrupted requests make it incomplete. Dial failure, rejected
handshake, admission failure and missing completion leave pending obligations.
No headers or transport error text are retained as evidence.

Run advisory locks and app locks serialize enrollment, request admission,
completion and sealing. After supported managed work settles, sealing commits
an irreversible admission fence. Existing obligations may finish, while new
recorders and requests are refused. Each owner stops admission, shuts down idle
HTTP connections, joins active parsers/handlers, and acknowledges closing only
when all persisted obligations have settled. Missing acknowledgement stays
pending until the CLI's bounded collector reports inconclusive. A failed session
cannot be converted to closed. SQL fences preserve identities and terminal
receipts and reject late mutation after sealing. Retention follows the app's
existing evidence boundary through namespace cleanup and soft deletion.

The protocol bounds retained sessions and obligations to 256 each per run.
Overflow raises the retained simulation failure marker and refuses admission.

## Supported boundary and qualification

The barrier covers serving-VM relay execution and the existing managed-work
inventory. Recipes still declare when the application operation has finished;
arbitrary timers and external queues are not discovered or drained. Host shell
commands, builders, separate job VMs and standalone Runs are outside this
recorder contract. Client failures before the host relay, including a failed
guest loopback proxy, still require recipe assertions. PostgreSQL/object-store behavior collectors and contained
command execution remain later increments.

Keep `FAAS_REHEARSAL_NETWORK_CONTAINMENT` and
`FAAS_REHEARSAL_ACTION_SIMULATION` unset until dedicated native x86_64 KVM
qualification covers boot/resume, relay settlement, owner loss, broker/storage
failure, concurrent closing and leakcheck. Upgrade vmmd, outboundd and apid
together; mixed versions refuse the new protocol or yield incomplete evidence.
