from typing import Literal

ScenarioSuspendResumeReceiptState = Literal[
    "armed", "claimed", "complete", "failed", "fenced", "paused", "resumed", "resuming", "waiting"
]

SCENARIO_SUSPEND_RESUME_RECEIPT_STATE_VALUES: set[ScenarioSuspendResumeReceiptState] = {
    "armed",
    "claimed",
    "complete",
    "failed",
    "fenced",
    "paused",
    "resumed",
    "resuming",
    "waiting",
}


def check_scenario_suspend_resume_receipt_state(value: str) -> ScenarioSuspendResumeReceiptState:
    if value in SCENARIO_SUSPEND_RESUME_RECEIPT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_RECEIPT_STATE_VALUES!r}")
