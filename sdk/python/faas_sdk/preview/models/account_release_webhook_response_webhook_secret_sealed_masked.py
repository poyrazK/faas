from typing import Literal

AccountReleaseWebhookResponseWebhookSecretSealedMasked = Literal["***"]

ACCOUNT_RELEASE_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES: set[
    AccountReleaseWebhookResponseWebhookSecretSealedMasked
] = {
    "***",
}


def check_account_release_webhook_response_webhook_secret_sealed_masked(
    value: str,
) -> AccountReleaseWebhookResponseWebhookSecretSealedMasked:
    if value in ACCOUNT_RELEASE_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_RELEASE_WEBHOOK_RESPONSE_WEBHOOK_SECRET_SEALED_MASKED_VALUES!r}"
    )
