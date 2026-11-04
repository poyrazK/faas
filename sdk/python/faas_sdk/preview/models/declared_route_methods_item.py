from typing import Literal

DeclaredRouteMethodsItem = Literal["CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"]

DECLARED_ROUTE_METHODS_ITEM_VALUES: set[DeclaredRouteMethodsItem] = {
    "CONNECT",
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
    "TRACE",
}


def check_declared_route_methods_item(value: str) -> DeclaredRouteMethodsItem:
    if value in DECLARED_ROUTE_METHODS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DECLARED_ROUTE_METHODS_ITEM_VALUES!r}")
