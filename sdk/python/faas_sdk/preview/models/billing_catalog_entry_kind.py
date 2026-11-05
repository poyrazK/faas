from typing import Literal

BillingCatalogEntryKind = Literal["monthly", "overage", "product"]

BILLING_CATALOG_ENTRY_KIND_VALUES: set[BillingCatalogEntryKind] = {
    "monthly",
    "overage",
    "product",
}


def check_billing_catalog_entry_kind(value: str) -> BillingCatalogEntryKind:
    if value in BILLING_CATALOG_ENTRY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_CATALOG_ENTRY_KIND_VALUES!r}")
