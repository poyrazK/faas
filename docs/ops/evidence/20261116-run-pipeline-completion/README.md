# Pipeline completion qualification

Scoped qualification passed on `gregale-internal-test-1` in `gregale-prod`,
`us-east1-b`, completed 2026-11-17 Europe/Istanbul (2026-11-16 UTC).
The feature remains default-off. No production deployment or activation has been
performed.

| Check | Result | Evidence |
| --- | --- | --- |
| Pipeline and Agent domain suites | Passed | [Domain log](domain.log) |
| Selected memory/PostgreSQL transaction suites | Passed | [Memory](state-mem.log), [PostgreSQL](state-pg.log) |
| Pipeline/Agent API restart, rollback and replay cases | Passed | [API log](api.log) |
| API contract and test-store inventory | Passed | [Contracts](contracts.log) |
| SDK route coverage and standalone Go preview test | Passed | [Coverage](sdk-coverage.log), [Go SDK](go-sdk.log) |
| Linux lint: runpipeline, agents, state, api, apidserver | Zero issues in all five groups | [Final server log](lint-apidserver.log) |
| SQLC determinism and seven expand/contract migrations | Passed | [SQLC](sqlc.log), [Migrations](migration-lint.log) |
| TypeScript build and three Node SDK tests | Passed | [Build](node-sdk-build.log), [Tests](node-sdk-tests.log) |
| Five Python SDK tests and nine Node starter tests | Passed | [Python](python-sdk-tests.log), [Starter](starter.log) |

The lane uses a private copy of the preceding Run pipeline qualification source,
with reviewed, checksum-verified deltas. All 368 implementation/SDK files in
[the source manifest](qualified-source-manifest.json) and
[the SDK manifest](qualified-sdk-manifest.json) match the node; see
[the verification output](source-verification.log). The final public SDK receipt
bindings were regenerated after the Linux checks and their focused Node/Python
checks repeated. The filtered SDK spec matches the Linux-generated spec; all 361
handwritten Python inputs retain their pre-regeneration hashes. Executable Go
source is unchanged from the passing Linux runs.

The tests cover immutable manifests, physical attempts, retry usage and artifact
references, standalone contract verdicts, atomic account webhook capture, N-1
terminal-writer fencing, endpoint/customer attribution, replay with writer gates
off, Agent JSONB key reordering, large-integer identity, provider-call conflicts,
lost acknowledgements, handler/worker reconstruction, revocation, and scoped
receipt access. PostgreSQL workflow reads now return the complete scoped receipt
through SQLC, including failure classification, finish time and artifact metadata.
Delivery uses stable inbox identities under an at-least-once contract; external
application effects still require application deduplication.

Behavioral builds omit duplicate default vet work; the required scoped Linux
lint pass includes govet. PostgreSQL tests use disposable databases under the
existing local `postgres` account, with explicit cleanup of their own test
PID's template/clone databases. The final cleanup query found zero matching
databases. No guest or VM lifecycle protocol changes are
introduced. These are control-plane functional and transaction checks, with
handler/worker recovery tests; they make no new native bare-metal or production
qualification claim.

See [the report](report.json), [final Linux status](linux-qualification.json) and
retained check logs. The executed commands are retained for the
[behavioral suites](commands/full-receipt-tests.sh),
[resumed contract/SDK checks](commands/contracts-and-lint.sh) and
[final three lint groups](commands/remaining-lint.sh); the first two lint groups
passed in the preceding invocation. These scoped checks do not constitute a full
repository test run. Documentation and this evidence report were updated after
the implementation snapshot.
