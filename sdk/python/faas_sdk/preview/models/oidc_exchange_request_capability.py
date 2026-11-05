from typing import Literal

OIDCExchangeRequestCapability = Literal["environment-preflight"]

OIDC_EXCHANGE_REQUEST_CAPABILITY_VALUES: set[OIDCExchangeRequestCapability] = {
    "environment-preflight",
}


def check_oidc_exchange_request_capability(value: str) -> OIDCExchangeRequestCapability:
    if value in OIDC_EXCHANGE_REQUEST_CAPABILITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OIDC_EXCHANGE_REQUEST_CAPABILITY_VALUES!r}")
