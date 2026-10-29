from typing import Literal

BillingCatalogEntryPlan = Literal["hobby", "pro", "scale"]

BILLING_CATALOG_ENTRY_PLAN_VALUES: set[BillingCatalogEntryPlan] = {
    "hobby",
    "pro",
    "scale",
}


def check_billing_catalog_entry_plan(value: str) -> BillingCatalogEntryPlan:
    if value in BILLING_CATALOG_ENTRY_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_CATALOG_ENTRY_PLAN_VALUES!r}")
