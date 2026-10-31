from typing import Literal

BillingRunItemResponseSourceKind = Literal["app_consumer", "platform_tenant", "product_control"]

BILLING_RUN_ITEM_RESPONSE_SOURCE_KIND_VALUES: set[BillingRunItemResponseSourceKind] = {
    "app_consumer",
    "platform_tenant",
    "product_control",
}


def check_billing_run_item_response_source_kind(value: str) -> BillingRunItemResponseSourceKind:
    if value in BILLING_RUN_ITEM_RESPONSE_SOURCE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BILLING_RUN_ITEM_RESPONSE_SOURCE_KIND_VALUES!r}")
