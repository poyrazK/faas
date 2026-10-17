# ADR-1292 · Guided live automation assertions

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Extend the saved case editor with separate live assertion controls
  over the existing `live` case object. Keep advanced test JSON as the complete
  editing surface, without adding another storage or execution contract.
- **Why:** Single-case live verification required writing its assertion structure
  as JSON even when the case's mock assertions could be edited through controls.
- **Consequences:**
  - Authors can add, select and remove live assertions, choose real terminal step
    states, assert exact JSON output and durable attempt counts, select loop
    items, and set the expected terminal run state. Mock and live selections are
    independent; live controls never translate mock success into real success.
  - Adding an assertion starts with no expected checks. Saving incomplete checks
    remains possible while authoring; live admission still rejects them. Editing,
    navigation, duplication and disabling live checks change only the form.
    Saving uses the existing permissions and version fences. Explicit real-action
    confirmation remains required to admit execution.
  - Guided edits patch the selected live assertion while retaining other live
    assertions, mock expectations, conditions, retry details and input/mocks.
    JSON RawMessage and pointer fields preserve large numbers, explicit null,
    count zero and omitted checks without browser JSON parsing.
  - Advanced test JSON remains authoritative when selected. Switching from JSON
    loads controls without applying stale guided fields. The older raw live form
    field remains accepted when no guided live source is posted.
  - Posted values and the original selected live assertion survive validation
    failures. Removing an assertion or disabling local live verification can
    discard invalid fields being removed. Duplication deep-copies live checks;
    case navigation resets the selected live assertion.
  - Existing central bounds apply: 128 live assertions, 3 MiB per live object,
    1 MiB per expected output, loop indices 0–127 and durable attempt counts
    through 425. Only one live assertion is expanded. The existing form budget's
    selected live JSON allowance covers the smaller guided output controls.
  - No migration, API, SDK, executor or VM lifecycle changes are introduced.
    Browser qualification uses fixture outcomes; it does not qualify deployed
    handlers or connected providers.
- **Rejected alternatives:** Sharing mock state controls permits meaningless live
  states. Automatically copying observed outputs approves results without review.
  Editing JSON in the browser can round integers and silently rewrite expectations.
