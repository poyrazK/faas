from typing import Literal

OAuthTokenExchangeErrorError = Literal[
    "invalid_grant",
    "invalid_request",
    "invalid_scope",
    "invalid_target",
    "temporarily_unavailable",
    "unsupported_subject_token_type",
    "unsupported_token_type",
]

O_AUTH_TOKEN_EXCHANGE_ERROR_ERROR_VALUES: set[OAuthTokenExchangeErrorError] = {
    "invalid_grant",
    "invalid_request",
    "invalid_scope",
    "invalid_target",
    "temporarily_unavailable",
    "unsupported_subject_token_type",
    "unsupported_token_type",
}


def check_o_auth_token_exchange_error_error(value: str) -> OAuthTokenExchangeErrorError:
    if value in O_AUTH_TOKEN_EXCHANGE_ERROR_ERROR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_ERROR_ERROR_VALUES!r}")
