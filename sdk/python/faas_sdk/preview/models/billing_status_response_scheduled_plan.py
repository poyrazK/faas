from typing import Literal

BillingStatusResponseScheduledPlan = Literal["free", "hobby", "pro", "scale"]

BILLING_STATUS_RESPONSE_SCHEDULED_PLAN_VALUES: set[BillingStatusResponseScheduledPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_billing_status_response_scheduled_plan(value: str) -> BillingStatusResponseScheduledPlan:
    if value in BILLING_STATUS_RESPONSE_SCHEDULED_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_STATUS_RESPONSE_SCHEDULED_PLAN_VALUES!r}")
