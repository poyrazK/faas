from typing import Literal

ServiceAuthorizationCheckResult = Literal["allowed", "denied", "indeterminate"]

SERVICE_AUTHORIZATION_CHECK_RESULT_VALUES: set[ServiceAuthorizationCheckResult] = {
    "allowed",
    "denied",
    "indeterminate",
}


def check_service_authorization_check_result(value: str) -> ServiceAuthorizationCheckResult:
    if value in SERVICE_AUTHORIZATION_CHECK_RESULT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_AUTHORIZATION_CHECK_RESULT_VALUES!r}")
