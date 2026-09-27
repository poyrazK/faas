from typing import Literal

ServiceCallerJWKKty = Literal["OKP"]

SERVICE_CALLER_JWK_KTY_VALUES: set[ServiceCallerJWKKty] = {
    "OKP",
}


def check_service_caller_jwk_kty(value: str) -> ServiceCallerJWKKty:
    if value in SERVICE_CALLER_JWK_KTY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_CALLER_JWK_KTY_VALUES!r}")
