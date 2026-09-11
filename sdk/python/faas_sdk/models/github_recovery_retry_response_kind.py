from typing import Literal

GithubRecoveryRetryResponseKind = Literal["check_update", "delivery"]

GITHUB_RECOVERY_RETRY_RESPONSE_KIND_VALUES: set[GithubRecoveryRetryResponseKind] = {
    "check_update",
    "delivery",
}


def check_github_recovery_retry_response_kind(value: str) -> GithubRecoveryRetryResponseKind:
    if value in GITHUB_RECOVERY_RETRY_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GITHUB_RECOVERY_RETRY_RESPONSE_KIND_VALUES!r}")
