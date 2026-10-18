This fixture exercises one contained import across a real running-worker pause
and a managed HTTP retry. It uses a dedicated rehearsal PostgreSQL database,
an isolated object namespace and simulated email; it has no provider transport.

The first attempt commits `pending → committed`, writes
`exports/import-001.csv`, waits for `after-commit`, simulates one email and
returns 503. The retry commits `committed → complete`. The baseline suppresses
another email; the candidate sends it again. Both final rows and CSV bytes match.
The independent action comparison must detect the duplicate.

The coordinator creates a separate Git repository from the explicit fixture
file inventory. It commits the baseline, changes only
`worker/revision.json` to `{"duplicate_notification":true}`, and commits the
candidate. Source commits and trusted input hashes remain in the evidence. It
does not modify the platform checkout or its Git references. Dependency versions
are locked; customer builds and scenario commands run in microVMs.

Portable checks, from the platform repository:

```bash
node tests/scenario-acceptance/rehearsal-running-job/worker/importer.test.mjs
python3 scripts/ci/rehearsal-native_test.py
go test -race ./scripts/ci/rehearsalacceptance
go test ./cmd/gregale -run '^TestRunningJobNativeQualificationFixture$'
```

Prepare the pinned source repository without provisioning anything:

```bash
python3 scripts/ci/rehearsal-native.py --prepare-only --out /tmp/rehearsal-preparation-UNIQUE
```

This writes `preparation.json` with `native_qualified: false`. Run the actual
native journey using the dedicated-host instructions in
[docs/rehearsal-native-acceptance.md](../../../docs/rehearsal-native-acceptance.md).
All rehearsal rollout gates remain disabled pending their native acceptance.
