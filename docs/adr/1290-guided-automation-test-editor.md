# ADR-1290 · Guided automation test case editor

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Add server-rendered case management and step assertion controls
  over the existing saved-suite contract. Retain advanced JSON as an explicit
  editing mode. Report the names of failed assertion fields without their values.
- **Why:** Reusable tests required authoring complete scenario arrays, and retained
  results could not explain whether output, state, or retry expectations failed.
- **Consequences:**
  - Adding, duplicating, selecting, renaming, and removing cases or assertions
    changes only the submitted form. Saving, running, and publication use the
    existing API, permissions, version fences, and saved-input checks.
  - Guided editing patches a selected case/assertion. It retains condition/reason
    assertions, detailed retry sequences, loop mocks, and exact JSON numbers.
    Explicit JSON null and attempt count zero remain distinct from omitted checks.
    New assertions require an explicit expected field; they do not approve output.
  - Advanced JSON takes precedence when selected. Switching from JSON loads the
    guided controls without applying stale controls. Posted fields survive errors.
    Only one case and assertion are expanded to bound rendered controls.
  - The shared assertion comparator returns stable mismatch field names. Reports
    may include `step`, `state`, `reason`, `when_matched`, `output`, `attempt_count`,
    and `attempts`. No expected/actual payload or error value enters those fields.
    Old reports remain readable and can be rerun for details. Overall passing still
    requires a valid, complete simulation without issues.
  - The optional report field extends Go, OpenAPI, Node, and Python contracts.
    Existing JSONB report storage needs no migration. Form limits account for
    percent-encoded selected-case input and expected output controls.
  - Mock acceptance does not certify deployed handlers or provider behavior.
- **Rejected alternatives:** Client-side JSON parsing can round large integers.
  Capturing observed output as automatically approved expectations hides mistakes.
  Retaining payload diffs increases disclosure and storage without being necessary
  to identify the failing assertion field.
