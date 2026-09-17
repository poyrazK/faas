from typing import Literal

OAuthTokenExchangeRequestSubjectTokenType = Literal["urn:ietf:params:oauth:token-type:jwt"]

O_AUTH_TOKEN_EXCHANGE_REQUEST_SUBJECT_TOKEN_TYPE_VALUES: set[OAuthTokenExchangeRequestSubjectTokenType] = {
    "urn:ietf:params:oauth:token-type:jwt",
}


def check_o_auth_token_exchange_request_subject_token_type(value: str) -> OAuthTokenExchangeRequestSubjectTokenType:
    if value in O_AUTH_TOKEN_EXCHANGE_REQUEST_SUBJECT_TOKEN_TYPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {O_AUTH_TOKEN_EXCHANGE_REQUEST_SUBJECT_TOKEN_TYPE_VALUES!r}"
    )
