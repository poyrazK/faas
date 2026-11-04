from typing import Literal

DebugRegressionActionRequestAction = Literal["acknowledge", "dismiss", "reopen", "resolve"]

DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES: set[DebugRegressionActionRequestAction] = {
    "acknowledge",
    "dismiss",
    "reopen",
    "resolve",
}


def check_debug_regression_action_request_action(value: str) -> DebugRegressionActionRequestAction:
    if value in DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES!r}")
