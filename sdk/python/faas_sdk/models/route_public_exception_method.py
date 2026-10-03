from typing import Literal

RoutePublicExceptionMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_PUBLIC_EXCEPTION_METHOD_VALUES: set[RoutePublicExceptionMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_public_exception_method(value: str) -> RoutePublicExceptionMethod:
    if value in ROUTE_PUBLIC_EXCEPTION_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_PUBLIC_EXCEPTION_METHOD_VALUES!r}")
