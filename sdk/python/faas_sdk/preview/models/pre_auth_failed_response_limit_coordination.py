from typing import Literal

PreAuthFailedResponseLimitCoordination = Literal["central", "local"]

PRE_AUTH_FAILED_RESPONSE_LIMIT_COORDINATION_VALUES: set[PreAuthFailedResponseLimitCoordination] = {
    "central",
    "local",
}


def check_pre_auth_failed_response_limit_coordination(value: str) -> PreAuthFailedResponseLimitCoordination:
    if value in PRE_AUTH_FAILED_RESPONSE_LIMIT_COORDINATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PRE_AUTH_FAILED_RESPONSE_LIMIT_COORDINATION_VALUES!r}"
    )
