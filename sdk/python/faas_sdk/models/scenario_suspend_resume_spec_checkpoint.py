from typing import Literal

ScenarioSuspendResumeSpecCheckpoint = Literal["after_setup", "managed_job"]

SCENARIO_SUSPEND_RESUME_SPEC_CHECKPOINT_VALUES: set[ScenarioSuspendResumeSpecCheckpoint] = {
    "after_setup",
    "managed_job",
}


def check_scenario_suspend_resume_spec_checkpoint(value: str) -> ScenarioSuspendResumeSpecCheckpoint:
    if value in SCENARIO_SUSPEND_RESUME_SPEC_CHECKPOINT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_SPEC_CHECKPOINT_VALUES!r}")
