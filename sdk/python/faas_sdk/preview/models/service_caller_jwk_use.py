from typing import Literal

ServiceCallerJWKUse = Literal["sig"]

SERVICE_CALLER_JWK_USE_VALUES: set[ServiceCallerJWKUse] = {
    "sig",
}


def check_service_caller_jwk_use(value: str) -> ServiceCallerJWKUse:
    if value in SERVICE_CALLER_JWK_USE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_CALLER_JWK_USE_VALUES!r}")
