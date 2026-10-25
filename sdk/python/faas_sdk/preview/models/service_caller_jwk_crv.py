from typing import Literal

ServiceCallerJWKCrv = Literal["Ed25519"]

SERVICE_CALLER_JWK_CRV_VALUES: set[ServiceCallerJWKCrv] = {
    "Ed25519",
}


def check_service_caller_jwk_crv(value: str) -> ServiceCallerJWKCrv:
    if value in SERVICE_CALLER_JWK_CRV_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_CALLER_JWK_CRV_VALUES!r}")
