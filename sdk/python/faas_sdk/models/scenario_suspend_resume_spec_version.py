from typing import Literal

ScenarioSuspendResumeSpecVersion = Literal[1]

SCENARIO_SUSPEND_RESUME_SPEC_VERSION_VALUES: set[ScenarioSuspendResumeSpecVersion] = {
    1,
}


def check_scenario_suspend_resume_spec_version(value: int) -> ScenarioSuspendResumeSpecVersion:
    if value in SCENARIO_SUSPEND_RESUME_SPEC_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_SUSPEND_RESUME_SPEC_VERSION_VALUES!r}")
