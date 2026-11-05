from typing import Literal

ManagedPostgresUsageOperatorResponseGuardrailState = Literal["disabled", "healthy", "reached", "stale"]

MANAGED_POSTGRES_USAGE_OPERATOR_RESPONSE_GUARDRAIL_STATE_VALUES: set[
    ManagedPostgresUsageOperatorResponseGuardrailState
] = {
    "disabled",
    "healthy",
    "reached",
    "stale",
}


def check_managed_postgres_usage_operator_response_guardrail_state(
    value: str,
) -> ManagedPostgresUsageOperatorResponseGuardrailState:
    if value in MANAGED_POSTGRES_USAGE_OPERATOR_RESPONSE_GUARDRAIL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_OPERATOR_RESPONSE_GUARDRAIL_STATE_VALUES!r}"
    )
