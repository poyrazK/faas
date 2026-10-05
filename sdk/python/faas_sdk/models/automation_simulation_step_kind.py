from typing import Literal

AutomationSimulationStepKind = Literal[
    "callback_wait", "condition_wait", "duration_wait", "event_wait", "for_each", "join", "outbound", "path", "run"
]

AUTOMATION_SIMULATION_STEP_KIND_VALUES: set[AutomationSimulationStepKind] = {
    "callback_wait",
    "condition_wait",
    "duration_wait",
    "event_wait",
    "for_each",
    "join",
    "outbound",
    "path",
    "run",
}


def check_automation_simulation_step_kind(value: str) -> AutomationSimulationStepKind:
    if value in AUTOMATION_SIMULATION_STEP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {AUTOMATION_SIMULATION_STEP_KIND_VALUES!r}")
