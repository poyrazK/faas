from typing import Literal

ServiceRolloutBindingGateAction = Literal["abort", "promote"]

SERVICE_ROLLOUT_BINDING_GATE_ACTION_VALUES: set[ServiceRolloutBindingGateAction] = {
    "abort",
    "promote",
}


def check_service_rollout_binding_gate_action(value: str) -> ServiceRolloutBindingGateAction:
    if value in SERVICE_ROLLOUT_BINDING_GATE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ROLLOUT_BINDING_GATE_ACTION_VALUES!r}")
