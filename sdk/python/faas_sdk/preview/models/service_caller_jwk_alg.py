from typing import Literal

ServiceCallerJWKAlg = Literal["EdDSA"]

SERVICE_CALLER_JWK_ALG_VALUES: set[ServiceCallerJWKAlg] = {
    "EdDSA",
}


def check_service_caller_jwk_alg(value: str) -> ServiceCallerJWKAlg:
    if value in SERVICE_CALLER_JWK_ALG_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_CALLER_JWK_ALG_VALUES!r}")
