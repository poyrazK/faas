from typing import Literal

PlanDetectedByMergedFromItem = Literal["app_yaml", "compose", "fly", "k8s", "other", "procfile", "render", "serverless"]

PLAN_DETECTED_BY_MERGED_FROM_ITEM_VALUES: set[PlanDetectedByMergedFromItem] = {
    "app_yaml",
    "compose",
    "fly",
    "k8s",
    "other",
    "procfile",
    "render",
    "serverless",
}


def check_plan_detected_by_merged_from_item(value: str) -> PlanDetectedByMergedFromItem:
    if value in PLAN_DETECTED_BY_MERGED_FROM_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_DETECTED_BY_MERGED_FROM_ITEM_VALUES!r}")
