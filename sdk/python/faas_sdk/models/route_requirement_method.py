from typing import Literal

RouteRequirementMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_REQUIREMENT_METHOD_VALUES: set[RouteRequirementMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_requirement_method(value: str) -> RouteRequirementMethod:
    if value in ROUTE_REQUIREMENT_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REQUIREMENT_METHOD_VALUES!r}")
