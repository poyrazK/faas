from typing import Literal

PlatformTenantWebhookResponseEventFilterItem = Literal["platform_tenant.statement.finalized"]

PLATFORM_TENANT_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES: set[PlatformTenantWebhookResponseEventFilterItem] = {
    "platform_tenant.statement.finalized",
}


def check_platform_tenant_webhook_response_event_filter_item(
    value: str,
) -> PlatformTenantWebhookResponseEventFilterItem:
    if value in PLATFORM_TENANT_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES!r}"
    )
