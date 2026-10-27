from typing import Literal

RecoverRolloutRequestAction = Literal["abort", "advance", "promote"]

RECOVER_ROLLOUT_REQUEST_ACTION_VALUES: set[RecoverRolloutRequestAction] = {
    "abort",
    "advance",
    "promote",
}


def check_recover_rollout_request_action(value: str) -> RecoverRolloutRequestAction:
    if value in RECOVER_ROLLOUT_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RECOVER_ROLLOUT_REQUEST_ACTION_VALUES!r}")
