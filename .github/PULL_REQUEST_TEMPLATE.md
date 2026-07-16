<!--
  PR template for poyrazK/faas.
  CLAUDE.md says: "PRs small enough to review in ten minutes; every PR
  names the milestone and, if it touches architecture, an ADR."
  Replace the placeholder below with the actual milestone + ADR link.
-->

## Milestone

<!-- Replace this line with M0 / M1 / M2 / ... -->

## What

<!-- 1-3 sentences. What changed and why? -->

## Spec / ADR impact

<!-- If this PR touches architecture or quota/limit decisions, link the
     docs/adr/* file. Everything else: write "none". -->

- Spec references:
- ADR:

## Verification

- [ ] `make test` — unit tests pass under `-race`
- [ ] `make lint` — golangci-lint clean
- [ ] `make proto-check` — checked-in *.pb.go matches codegen
- [ ] `make build` — all 8 daemons build
- [ ] (If metal-tagged test touched) `sudo make test-metal` on EX44
- [ ] (If quota/limit changed) `pkg/api/limits.go` updated, ADR cited in docs/adr/README.md
