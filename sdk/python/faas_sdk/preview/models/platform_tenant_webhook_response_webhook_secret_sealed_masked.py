from typing import Literal

PlatformTenantWebhookResponseWebhookSecretSealedMasked = Literal["***"]

PLATFORM_TENANT_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES: set[
    PlatformTenantWebhookResponseWebhookSecretSealedMasked
] = {
    "***",
}


def check_platform_tenant_webhook_response_webhook_secret_sealed_masked(
    value: str,
) -> PlatformTenantWebhookResponseWebhookSecretSealedMasked:
    if value in PLATFORM_TENANT_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES!r}"
    )
