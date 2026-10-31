from typing import Literal

PreAuthRouteLimitMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

PRE_AUTH_ROUTE_LIMIT_METHOD_VALUES: set[PreAuthRouteLimitMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_pre_auth_route_limit_method(value: str) -> PreAuthRouteLimitMethod:
    if value in PRE_AUTH_ROUTE_LIMIT_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PRE_AUTH_ROUTE_LIMIT_METHOD_VALUES!r}")
