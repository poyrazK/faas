from typing import Literal

OAuthTokenExchangeRequestGrantType = Literal["urn:ietf:params:oauth:grant-type:token-exchange"]

O_AUTH_TOKEN_EXCHANGE_REQUEST_GRANT_TYPE_VALUES: set[OAuthTokenExchangeRequestGrantType] = {
    "urn:ietf:params:oauth:grant-type:token-exchange",
}


def check_o_auth_token_exchange_request_grant_type(value: str) -> OAuthTokenExchangeRequestGrantType:
    if value in O_AUTH_TOKEN_EXCHANGE_REQUEST_GRANT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_REQUEST_GRANT_TYPE_VALUES!r}")
