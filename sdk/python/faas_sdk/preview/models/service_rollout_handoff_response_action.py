from typing import Literal

ServiceRolloutHandoffResponseAction = Literal["abort", "promote"]

SERVICE_ROLLOUT_HANDOFF_RESPONSE_ACTION_VALUES: set[ServiceRolloutHandoffResponseAction] = {
    "abort",
    "promote",
}


def check_service_rollout_handoff_response_action(value: str) -> ServiceRolloutHandoffResponseAction:
    if value in SERVICE_ROLLOUT_HANDOFF_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ROLLOUT_HANDOFF_RESPONSE_ACTION_VALUES!r}")
