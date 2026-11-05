from typing import Literal

OAuthTokenExchangeResponseTokenType = Literal["Bearer"]

O_AUTH_TOKEN_EXCHANGE_RESPONSE_TOKEN_TYPE_VALUES: set[OAuthTokenExchangeResponseTokenType] = {
    "Bearer",
}


def check_o_auth_token_exchange_response_token_type(value: str) -> OAuthTokenExchangeResponseTokenType:
    if value in O_AUTH_TOKEN_EXCHANGE_RESPONSE_TOKEN_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_RESPONSE_TOKEN_TYPE_VALUES!r}")
