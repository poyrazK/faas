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
| `make test-mutation-ledger` | `pkg/sched/admission*.go` (the schedd admission ledger, §6.2 invariants 1–2) | Test funcs in `pkg/sched/admission*_test.go` and `*ledger*_test.go`, then all of `tests/property` |

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
alter behaviour, e.g. `if q < 0 { return 0 }` → `q <= 0` when q == 0 already
yields 0. Leave equivalent mutants alone. Once the survivor list is triaged,
set `-min-efficacy` in the workflow to the measured level so regressions fail
the weekly run.
