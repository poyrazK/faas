from typing import Literal

CreateRouteSplitRequestKind = Literal["cpu", "memory"]

CREATE_ROUTE_SPLIT_REQUEST_KIND_VALUES: set[CreateRouteSplitRequestKind] = {
    "cpu",
    "memory",
}


def check_create_route_split_request_kind(value: str) -> CreateRouteSplitRequestKind:
    if value in CREATE_ROUTE_SPLIT_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ROUTE_SPLIT_REQUEST_KIND_VALUES!r}")
