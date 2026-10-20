from typing import Literal

BuildPlanFramework = Literal["docker", "go", "node", "python", "unknown"]

BUILD_PLAN_FRAMEWORK_VALUES: set[BuildPlanFramework] = {
    "docker",
    "go",
    "node",
    "python",
    "unknown",
}


def check_build_plan_framework(value: str) -> BuildPlanFramework:
    if value in BUILD_PLAN_FRAMEWORK_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_PLAN_FRAMEWORK_VALUES!r}")
