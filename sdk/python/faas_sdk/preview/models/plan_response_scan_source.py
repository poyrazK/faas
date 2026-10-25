from typing import Literal

PlanResponseScanSource = Literal[
    "compose", "convention", "fly", "k8s", "procfile", "render", "serverless", "single", "unknown", "workspace"
]

PLAN_RESPONSE_SCAN_SOURCE_VALUES: set[PlanResponseScanSource] = {
    "compose",
    "convention",
    "fly",
    "k8s",
    "procfile",
    "render",
    "serverless",
    "single",
    "unknown",
    "workspace",
}


def check_plan_response_scan_source(value: str) -> PlanResponseScanSource:
    if value in PLAN_RESPONSE_SCAN_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLAN_RESPONSE_SCAN_SOURCE_VALUES!r}")
