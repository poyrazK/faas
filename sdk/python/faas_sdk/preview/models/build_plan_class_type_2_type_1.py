from typing import Literal

BuildPlanClassType2Type1 = Literal["app", "function"]

BUILD_PLAN_CLASS_TYPE_2_TYPE_1_VALUES: set[BuildPlanClassType2Type1] = {
    "app",
    "function",
}


def check_build_plan_class_type_2_type_1(value: str) -> BuildPlanClassType2Type1:
    if value in BUILD_PLAN_CLASS_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_PLAN_CLASS_TYPE_2_TYPE_1_VALUES!r}")
