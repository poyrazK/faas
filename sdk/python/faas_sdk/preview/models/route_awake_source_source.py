from typing import Literal

RouteAwakeSourceSource = Literal["prewarm", "route_probe"]

ROUTE_AWAKE_SOURCE_SOURCE_VALUES: set[RouteAwakeSourceSource] = {
    "prewarm",
    "route_probe",
}


def check_route_awake_source_source(value: str) -> RouteAwakeSourceSource:
    if value in ROUTE_AWAKE_SOURCE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_AWAKE_SOURCE_SOURCE_VALUES!r}")
