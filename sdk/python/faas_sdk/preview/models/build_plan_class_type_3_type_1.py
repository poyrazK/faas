from typing import Literal

BuildPlanClassType3Type1 = Literal["app", "function"]

BUILD_PLAN_CLASS_TYPE_3_TYPE_1_VALUES: set[BuildPlanClassType3Type1] = {
    "app",
    "function",
}


def check_build_plan_class_type_3_type_1(value: str) -> BuildPlanClassType3Type1:
    if value in BUILD_PLAN_CLASS_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_PLAN_CLASS_TYPE_3_TYPE_1_VALUES!r}")
