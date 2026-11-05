from typing import Literal

AppWebhookResponseWebhookSecretSealedMasked = Literal["***"]

APP_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES: set[AppWebhookResponseWebhookSecretSealedMasked] = {
    "***",
}


def check_app_webhook_response_webhook_secret_sealed_masked(value: str) -> AppWebhookResponseWebhookSecretSealedMasked:
    if value in APP_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES!r}"
    )
