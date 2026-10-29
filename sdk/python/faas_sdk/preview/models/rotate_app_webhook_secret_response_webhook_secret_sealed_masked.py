from typing import Literal

RotateAppWebhookSecretResponseWebhookSecretSealedMasked = Literal["***"]

ROTATE_APP_WEBHOOK_SECRET_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES: set[
    RotateAppWebhookSecretResponseWebhookSecretSealedMasked
] = {
    "***",
}


def check_rotate_app_webhook_secret_response_webhook_secret_sealed_masked(
    value: str,
) -> RotateAppWebhookSecretResponseWebhookSecretSealedMasked:
    if value in ROTATE_APP_WEBHOOK_SECRET_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROTATE_APP_WEBHOOK_SECRET_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES!r}"
    )
