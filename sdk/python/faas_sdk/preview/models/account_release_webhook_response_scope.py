from typing import Literal

AccountReleaseWebhookResponseScope = Literal["account"]

ACCOUNT_RELEASE_WEBHOOK_RESPONSE_SCOPE_VALUES: set[AccountReleaseWebhookResponseScope] = {
    "account",
}


def check_account_release_webhook_response_scope(value: str) -> AccountReleaseWebhookResponseScope:
    if value in ACCOUNT_RELEASE_WEBHOOK_RESPONSE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACCOUNT_RELEASE_WEBHOOK_RESPONSE_SCOPE_VALUES!r}")
