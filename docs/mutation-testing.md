# Mutation testing

Line coverage says a test *ran* a line. Mutation testing says whether any test
would *notice* that line computing the wrong answer. `cmd/mutate` changes one
operator at a time, reruns the tests that own the code, and reports each
mutant as:

| Status | Meaning |
| --- | --- |
| killed | a test failed: the behaviour is pinned |
| lived | every test still passed: a gap, or an equivalent mutant |
| timed out | the mutant hung the tests (counted as killed) |
| not covered | the selected tests never execute the line; not run |
| not viable | the mutant does not compile; ignored |

Test efficacy = killed / (killed + lived). Mutant coverage = mutants on
executed lines / all mutants.

## Targets

| Make target | Mutated code | Tests run per mutant |
| --- | --- | --- |
| `make test-mutation-billing` | `pkg/billing/*.go` | all of `pkg/billing` |
| `make test-mutation-ledger` | `pkg/sched/admission*.go` (the schedd admission ledger, §6.2 invariants 1–2) | Test funcs in the ledger's test files (`admission*_test.go`, `*ledger*_test.go`, `flexible_reserve_test.go`, `stage_capacity_test.go`, `vcpu_combined_test.go`, `frozen_test.go`, `cpu_admission_test.go`, `state_machine_edges_test.go`; ~140 tests, ~3 s), then all of `tests/property` |

The ledger target does not run the whole `pkg/sched` suite (about two
minutes) per mutant; it runs the tests that own the ledger plus the property
invariants. To widen it, add test file globs to `-tests` in the Makefile.

Both are slow: every mutant recompiles its package. They run weekly in
`.github/workflows/mutation.yml` (sharded, report-only) and on dispatch. Not
in PR CI.

Locally:

```sh
make test-mutation-ledger MUTATE_FLAGS=-list          # list mutants + coverage, run nothing
make test-mutation-billing MUTATE_SHARD=1/20          # one twentieth of the mutants
make test-mutation-billing MUTATE_FLAGS='-json /tmp/billing.json'
go run ./cmd/mutate -h                                # ad-hoc targets
```

## Operators

`ARITHMETIC_BASE` (`+ - * / %` on numbers), `CONDITIONALS_BOUNDARY`
(`<`↔`<=`, `>`↔`>=`), `CONDITIONALS_NEGATION` (`==`↔`!=`, `<`↔`>=`, ...),
`INVERT_LOGICAL` (`&&`↔`||`), `INVERT_NOT` (drop `!`), `INVERT_NEGATIVES`
(drop unary `-`), `INCREMENT_DECREMENT`, `INVERT_ASSIGNMENTS` (`+=`↔`-=`,
`*=`↔`/=`). Constant expressions and string `+` are skipped using type
information.

## How it works

- Each mutant is passed to `go test -c -overlay`, so the checkout is never
  edited and parallel workers share the build cache.
- The build is untimed; only the test run is bounded (baseline run time ×
  `-timeout-coefficient` + 30 s), so a slow compile on a busy machine is not
  misreported as a hang.
- One baseline run with `-coverpkg` marks which lines the selected tests
  execute; mutants elsewhere are reported as not covered without running.

Gremlins and go-mutesting were tried first. Gremlins copies the module per
worker and runs a package's whole suite per mutant. go-mutesting needs a
newer Go than `go.mod` pins and edits files in place.

## Triaging survivors

A lived mutant is either a missing assertion (write the test, cite the
behaviour per `TESTOWNERS`) or an equivalent mutant: a change that cannot
alter behaviour, e.g. a clamp `if n < 0 { n = 0 }` → `n <= 0`. Leave
equivalent mutants alone.

The workflow's `gate` job merges each target's shard reports
(`go run ./cmd/mutate -merge 'reports/mutation-ledger-*/*.json'`) and fails
when efficacy falls below the target's floor:

| Target | First run (43699361433) | After triage (43714485891) | Floor |
| --- | --- | --- | --- |
| billing | 76.2% (924 killed, 288 lived, 174 not covered) | 78.0% (949 killed, 267 lived, 170 not covered) | 78% |
| ledger | 70.4% (247 killed, 104 lived, 128 not covered) | 79.8% (316 killed, 80 lived, 83 not covered) | 79% |

Raise a floor (in `mutation.yml`) after a run that triaged survivors lifts
the score. Largest remaining survivor groups: ledger `admission.go` (32,
many are clamp-to-zero equivalents), `admission_frozen.go` (12) and
`admission_environment_capacity.go` (10); billing
`platform_tenant_month_statements.go` (34), `api_consumer_pricing.go` (32,
mostly int64 overflow-guard boundaries), `api_consumer_statements.go` (25)
and `financial_visibility.go` (24). The latest run's per-shard job summaries
list each survivor with its source line.

Known equivalent mutants (no test can kill them): `admission_cpu_quota.go:13`
(boost and its deadline are cleared together), `admission.go:1148`
(`cpuCeiling > 0` → `>= 0`: no CPU check without a budget),
`api_consumer_pricing.go:416` (after the first tier `position` never exceeds
the previous bound), and in the billing money paths (2026-11-16 triage):
`spend_cap.go:46` (zero remaining floors to zero either way),
`credit_consume.go:81` (a half period also fails the range read),
`credit_consume.go:132` `>`→`>=` (capping at an equal target is a no-op),
`currency.go:52,67,82` (a step of 1, equal exponents and a zero amount all
round to themselves), `egress_overage.go:55,78` (a zero row adds nothing; the
saturation bound equals the sum), `statement_invoice.go:257` `<=`→`<` (a zero
fee line is dropped at finish), and the `<`→`<=` boundaries in the invoice
line sort at `statement_invoice.go:289-297` (each compares values the
preceding `!=` proved different). In API consumer pricing:
`api_consumer_pricing.go:366` `||`→`&&` (negative units still fail the
overflow test, since `maxInt64-units` wraps), `:469` (no allowance left
covers `min(0, units) = 0`) and `:488`/`:489` sign boundaries (a zero-amount
bucket adds nothing). The platform tenant month survivors left
(`platform_tenant_month_statements.go` `priceTenantMonth` and
`BuildTenantMonthStatement`) need a whole-month statement fixture.
