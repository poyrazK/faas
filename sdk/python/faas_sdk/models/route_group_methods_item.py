from typing import Literal

RouteGroupMethodsItem = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

ROUTE_GROUP_METHODS_ITEM_VALUES: set[RouteGroupMethodsItem] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_route_group_methods_item(value: str) -> RouteGroupMethodsItem:
    if value in ROUTE_GROUP_METHODS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_GROUP_METHODS_ITEM_VALUES!r}")
