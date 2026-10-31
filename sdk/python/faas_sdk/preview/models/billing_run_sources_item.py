from typing import Literal

BillingRunSourcesItem = Literal["app_consumers", "platform_tenants", "product_customers"]

BILLING_RUN_SOURCES_ITEM_VALUES: set[BillingRunSourcesItem] = {
    "app_consumers",
    "platform_tenants",
    "product_customers",
}


def check_billing_run_sources_item(value: str) -> BillingRunSourcesItem:
    if value in BILLING_RUN_SOURCES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_SOURCES_ITEM_VALUES!r}")
