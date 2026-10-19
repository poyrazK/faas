from typing import Literal

OrgQueueUsageResponsePlan = Literal["free", "hobby", "pro", "scale"]

ORG_QUEUE_USAGE_RESPONSE_PLAN_VALUES: set[OrgQueueUsageResponsePlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_org_queue_usage_response_plan(value: str) -> OrgQueueUsageResponsePlan:
    if value in ORG_QUEUE_USAGE_RESPONSE_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_QUEUE_USAGE_RESPONSE_PLAN_VALUES!r}")
