from typing import Literal

OAuthTokenExchangeRequestRequestedTokenType = Literal["urn:ietf:params:oauth:token-type:access_token"]

O_AUTH_TOKEN_EXCHANGE_REQUEST_REQUESTED_TOKEN_TYPE_VALUES: set[OAuthTokenExchangeRequestRequestedTokenType] = {
    "urn:ietf:params:oauth:token-type:access_token",
}


def check_o_auth_token_exchange_request_requested_token_type(value: str) -> OAuthTokenExchangeRequestRequestedTokenType:
    if value in O_AUTH_TOKEN_EXCHANGE_REQUEST_REQUESTED_TOKEN_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_REQUEST_REQUESTED_TOKEN_TYPE_VALUES!r}"
    )
