from typing import Literal

WorkflowDiagnosticStepKind = Literal["action", "callback", "condition", "event", "for_each", "join", "timer", "unknown"]

WORKFLOW_DIAGNOSTIC_STEP_KIND_VALUES: set[WorkflowDiagnosticStepKind] = {
    "action",
    "callback",
    "condition",
    "event",
    "for_each",
    "join",
    "timer",
    "unknown",
}


def check_workflow_diagnostic_step_kind(value: str) -> WorkflowDiagnosticStepKind:
    if value in WORKFLOW_DIAGNOSTIC_STEP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_DIAGNOSTIC_STEP_KIND_VALUES!r}")
