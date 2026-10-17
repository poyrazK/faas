from typing import Literal

BillingRunItemResponseOutcome = Literal[
    "drafted", "exported", "failed", "finalized", "held", "pending", "skipped", "up_to_date"
]

BILLING_RUN_ITEM_RESPONSE_OUTCOME_VALUES: set[BillingRunItemResponseOutcome] = {
    "drafted",
    "exported",
    "failed",
    "finalized",
    "held",
    "pending",
    "skipped",
    "up_to_date",
}


def check_billing_run_item_response_outcome(value: str) -> BillingRunItemResponseOutcome:
    if value in BILLING_RUN_ITEM_RESPONSE_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_ITEM_RESPONSE_OUTCOME_VALUES!r}")
