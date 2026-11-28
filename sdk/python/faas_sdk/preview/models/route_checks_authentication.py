from typing import Literal

RouteChecksAuthentication = Literal["application", "consumer", "jwt"]

ROUTE_CHECKS_AUTHENTICATION_VALUES: set[RouteChecksAuthentication] = {
    "application",
    "consumer",
    "jwt",
}


def check_route_checks_authentication(value: str) -> RouteChecksAuthentication:
    if value in ROUTE_CHECKS_AUTHENTICATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_CHECKS_AUTHENTICATION_VALUES!r}")
