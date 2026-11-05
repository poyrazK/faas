from typing import Literal

OAuthTokenExchangeResponseScope = Literal["deploy:write"]

O_AUTH_TOKEN_EXCHANGE_RESPONSE_SCOPE_VALUES: set[OAuthTokenExchangeResponseScope] = {
    "deploy:write",
}


def check_o_auth_token_exchange_response_scope(value: str) -> OAuthTokenExchangeResponseScope:
    if value in O_AUTH_TOKEN_EXCHANGE_RESPONSE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_RESPONSE_SCOPE_VALUES!r}")
