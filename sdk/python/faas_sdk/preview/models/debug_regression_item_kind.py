from typing import Literal

DebugRegressionItemKind = Literal["errors", "latency", "latency_and_errors"]

DEBUG_REGRESSION_ITEM_KIND_VALUES: set[DebugRegressionItemKind] = {
    "errors",
    "latency",
    "latency_and_errors",
}


def check_debug_regression_item_kind(value: str) -> DebugRegressionItemKind:
    if value in DEBUG_REGRESSION_ITEM_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REGRESSION_ITEM_KIND_VALUES!r}")
