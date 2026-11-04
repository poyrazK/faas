from typing import Literal

BillingRunItemResponseCompleteness = Literal["gaps_detected", "partial", "unverifiable", "verified"]

BILLING_RUN_ITEM_RESPONSE_COMPLETENESS_VALUES: set[BillingRunItemResponseCompleteness] = {
    "gaps_detected",
    "partial",
    "unverifiable",
    "verified",
}


def check_billing_run_item_response_completeness(value: str) -> BillingRunItemResponseCompleteness:
    if value in BILLING_RUN_ITEM_RESPONSE_COMPLETENESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_ITEM_RESPONSE_COMPLETENESS_VALUES!r}")
