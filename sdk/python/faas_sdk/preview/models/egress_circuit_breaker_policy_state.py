from typing import Literal

EgressCircuitBreakerPolicyState = Literal["closed", "half_open", "open"]

EGRESS_CIRCUIT_BREAKER_POLICY_STATE_VALUES: set[EgressCircuitBreakerPolicyState] = {
    "closed",
    "half_open",
    "open",
}


def check_egress_circuit_breaker_policy_state(value: str) -> EgressCircuitBreakerPolicyState:
    if value in EGRESS_CIRCUIT_BREAKER_POLICY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EGRESS_CIRCUIT_BREAKER_POLICY_STATE_VALUES!r}")
