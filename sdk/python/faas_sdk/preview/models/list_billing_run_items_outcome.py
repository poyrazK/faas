from typing import Literal

ListBillingRunItemsOutcome = Literal[
    "drafted", "exported", "failed", "finalized", "held", "pending", "skipped", "up_to_date"
]

LIST_BILLING_RUN_ITEMS_OUTCOME_VALUES: set[ListBillingRunItemsOutcome] = {
    "drafted",
    "exported",
    "failed",
    "finalized",
    "held",
    "pending",
    "skipped",
    "up_to_date",
}


def check_list_billing_run_items_outcome(value: str) -> ListBillingRunItemsOutcome:
    if value in LIST_BILLING_RUN_ITEMS_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_BILLING_RUN_ITEMS_OUTCOME_VALUES!r}")
