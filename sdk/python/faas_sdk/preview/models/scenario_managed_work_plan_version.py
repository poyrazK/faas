from typing import Literal

ScenarioManagedWorkPlanVersion = Literal[1]

SCENARIO_MANAGED_WORK_PLAN_VERSION_VALUES: set[ScenarioManagedWorkPlanVersion] = {
    1,
}


def check_scenario_managed_work_plan_version(value: int) -> ScenarioManagedWorkPlanVersion:
    if value in SCENARIO_MANAGED_WORK_PLAN_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCENARIO_MANAGED_WORK_PLAN_VERSION_VALUES!r}")
