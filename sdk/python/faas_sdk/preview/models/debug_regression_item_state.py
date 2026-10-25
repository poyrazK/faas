from typing import Literal

DebugRegressionItemState = Literal["acknowledged", "active", "dismissed", "resolved"]

DEBUG_REGRESSION_ITEM_STATE_VALUES: set[DebugRegressionItemState] = {
    "acknowledged",
    "active",
    "dismissed",
    "resolved",
}


def check_debug_regression_item_state(value: str) -> DebugRegressionItemState:
    if value in DEBUG_REGRESSION_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REGRESSION_ITEM_STATE_VALUES!r}")
