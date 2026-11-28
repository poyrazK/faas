from typing import Literal

OAuthTokenExchangeResponseIssuedTokenType = Literal["urn:ietf:params:oauth:token-type:access_token"]

O_AUTH_TOKEN_EXCHANGE_RESPONSE_ISSUED_TOKEN_TYPE_VALUES: set[OAuthTokenExchangeResponseIssuedTokenType] = {
    "urn:ietf:params:oauth:token-type:access_token",
}


def check_o_auth_token_exchange_response_issued_token_type(value: str) -> OAuthTokenExchangeResponseIssuedTokenType:
    if value in O_AUTH_TOKEN_EXCHANGE_RESPONSE_ISSUED_TOKEN_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_RESPONSE_ISSUED_TOKEN_TYPE_VALUES!r}"
    )
