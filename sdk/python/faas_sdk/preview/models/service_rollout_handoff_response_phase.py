from typing import Literal

ServiceRolloutHandoffResponsePhase = Literal["complete", "draining", "pending", "routing"]

SERVICE_ROLLOUT_HANDOFF_RESPONSE_PHASE_VALUES: set[ServiceRolloutHandoffResponsePhase] = {
    "complete",
    "draining",
    "pending",
    "routing",
}


def check_service_rollout_handoff_response_phase(value: str) -> ServiceRolloutHandoffResponsePhase:
    if value in SERVICE_ROLLOUT_HANDOFF_RESPONSE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SERVICE_ROLLOUT_HANDOFF_RESPONSE_PHASE_VALUES!r}")
