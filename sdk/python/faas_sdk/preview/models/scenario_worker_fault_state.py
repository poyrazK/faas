from typing import Literal

ScenarioWorkerFaultState = Literal["armed", "claimed", "failed", "fenced", "ready", "terminated"]

SCENARIO_WORKER_FAULT_STATE_VALUES: set[ScenarioWorkerFaultState] = {
    "armed",
    "claimed",
    "failed",
    "fenced",
    "ready",
    "terminated",
}


def check_scenario_worker_fault_state(value: str) -> ScenarioWorkerFaultState:
    if value in SCENARIO_WORKER_FAULT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_WORKER_FAULT_STATE_VALUES!r}")
