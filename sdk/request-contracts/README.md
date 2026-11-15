# Node/Python SDK behavior contracts

`async-options.json` is consumed by both the Node and Python SDK suites.
Each case specifies logical HTTP attempts, response IDs/statuses, failure kind,
and resource/identity expectations. Timeouts are milliseconds in the fixture;
the Python runner converts to seconds. Duration measurements must be finite
and nonnegative, and are intentionally not compared for exact equality.

Run from this repository:

```sh
npm --prefix sdk/node run test:build
node --test sdk/node/dist-test/test/async-request-contract.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_async_request_options.py
```

Both source mirrors include these fixtures; they are not runtime package data.
New cases must pass both runners before changing a shared behavioral contract.

`source-preparation.json` pins local worktree selection across Node, Python and
the CLI: default and ordered ignore rules, parent pruning, Dockerfile contexts,
Go vendoring, explicit workspace application roots, Unicode/long names, safe
links, scoped exclusions and strict dotenv findings. Secret cases join synthetic
`line_parts` at runtime so the fixtures do not store complete credential-shaped
values. The Go test compiles the
actual CLI selection and scanner declarations into a stdlib-only harness; it
does not maintain a third copy of the rules. Run that check from the platform
repository. SDK mirrors run the Node/Python fixtures independently.

```sh
go test sdk/request-contracts/cli-source-preparation_test.go
node --test sdk/node/dist-test/test/source-preparation.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_source_preparation.py
```

`pagination.json` runs against public and preview clients, for both items and
pages, in both SDKs. It specifies exact cursor strings (including nanoseconds,
offsets and opaque build keys), retained filters, request sizes, row/page caps,
empty pages, cursor cycles, invalid pages, blocked bodies, pending close,
Retry-After deadlines and Problem identity. A count limit never slices away a
resumable page boundary. `timeout_ms` is converted to Python seconds.

```sh
node --test sdk/node/dist-test/test/pagination-contract.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_pagination.py
```

`waiters.json` is run by both SDKs against public and preview clients. Its cases
cover build/release lifecycle states, blocked and terminal outcomes, unknown or
mismatched observations, poll caps, progress failures/deadlines, caller
cancellation, stalled headers/bodies, Retry-After budgets, retry-versus-poll
counts, typed Problem identity, and resume by the same resource ID. All reads
must use GET; no waiter may resubmit work. Millisecond fixture budgets and delays
are converted to Python seconds. Progress state sequences and resource outcomes
match across languages; Node freezes model snapshots and Python provides
independent typed model copies. Python timing metadata and options are frozen.

```sh
node --test sdk/node/dist-test/test/waiter-contract.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_waiters.py
```

`source-uploads.json` pins the common Node/Python source transfer and commit
recovery contract. Its public/preview scenarios cover durable restart, source and
metadata binding, accepted byte offsets, lost/partial/conflicting chunk replies,
creation idempotency, expiry, invalid acknowledgments, callback persistence
failures, commit receipt recovery, uncertain/pending acceptance, cancellation,
deadlines and recovery caps. The shared metadata fingerprint uses tagged JSON
values with binary64 big-endian number encodings and UTF-8 key order; metadata
presence is significant. Journals retain identities, not credentials or metadata.

```sh
node --test sdk/node/dist-test/test/source-upload-contract.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_source_upload.py
```

`source-deployments.json` covers the composed deployment workflow in both SDKs
and both public/preview surfaces. Interrupt every saved boundary, recover lost
chunk/commit replies, keep unknown acceptance explicit, retain one deployment,
reuse workspace archives, bind client/metadata/preparation state, and observe
blocked/failed releases. Fingerprints are identical across the two journal
implementations. Additional tests terminate real subprocesses at committing,
exercise concurrent accounts, preserve captured configuration, enforce one total
budget and reject corrupt state before remote writes.

```sh
node --test sdk/node/dist-test/test/source-deployment-contract.test.js
sdk/python/.venv/bin/python -m pytest sdk/python/tests/test_source_deployment.py
```
## Project inspection

`project-inspection.json` is shared by Node and Python, including portable plan
fingerprints. `project-inspection-cli_test.go` compiles the actual CLI marker and
workspace glob helpers against its CLI-tagged cases and pins the virtual Go
module's toolchain. Other cases verify the SDK inspection contract; deferred
resource validation and explicit ambiguity handling are intentional SDK limits.
Run the Go file together with `cli-source-preparation_test.go` from the platform
repository. Installed-package tests cover the public and preview exports.

## Reviewed project workflows

`project-workflows.json` drives both SDKs' account-owned local project lifecycle
tests: authoritative preview, exact-source cached apply and receipt-only recovery.
Its sparse receipt cases reflect actual server records, including processing
before workload selection and accepted images without build IDs. Individual
runners additionally test account concurrency, configuration snapshots, journal
ownership, total deadlines and cancellation. The backend cached-apply integration
test proves the optional attempt header enters the existing durable submission
path. The API/worker/PostgreSQL example and opt-in public journey exercise the
same SDK methods across process boundaries.
