from typing import Literal

GuardedDebugRegressionActionRequestAction = Literal["acknowledge", "dismiss", "reopen", "resolve"]

GUARDED_DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES: set[GuardedDebugRegressionActionRequestAction] = {
    "acknowledge",
    "dismiss",
    "reopen",
    "resolve",
}


def check_guarded_debug_regression_action_request_action(value: str) -> GuardedDebugRegressionActionRequestAction:
    if value in GUARDED_DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GUARDED_DEBUG_REGRESSION_ACTION_REQUEST_ACTION_VALUES!r}"
    )
