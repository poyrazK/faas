from typing import Literal

ServiceRolloutBindingGateStatus = Literal["blocked", "passed", "pending"]

SERVICE_ROLLOUT_BINDING_GATE_STATUS_VALUES: set[ServiceRolloutBindingGateStatus] = {
    "blocked",
    "passed",
    "pending",
}


def check_service_rollout_binding_gate_status(value: str) -> ServiceRolloutBindingGateStatus:
    if value in SERVICE_ROLLOUT_BINDING_GATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ROLLOUT_BINDING_GATE_STATUS_VALUES!r}")
