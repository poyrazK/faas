from typing import Literal

OAuthTokenExchangeRequestScope = Literal["deploy:write"]

O_AUTH_TOKEN_EXCHANGE_REQUEST_SCOPE_VALUES: set[OAuthTokenExchangeRequestScope] = {
    "deploy:write",
}


def check_o_auth_token_exchange_request_scope(value: str) -> OAuthTokenExchangeRequestScope:
    if value in O_AUTH_TOKEN_EXCHANGE_REQUEST_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_REQUEST_SCOPE_VALUES!r}")
