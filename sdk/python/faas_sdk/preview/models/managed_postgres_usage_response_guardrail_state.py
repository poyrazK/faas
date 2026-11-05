from typing import Literal

ManagedPostgresUsageResponseGuardrailState = Literal["disabled", "healthy", "reached", "stale"]

MANAGED_POSTGRES_USAGE_RESPONSE_GUARDRAIL_STATE_VALUES: set[ManagedPostgresUsageResponseGuardrailState] = {
    "disabled",
    "healthy",
    "reached",
    "stale",
}


def check_managed_postgres_usage_response_guardrail_state(value: str) -> ManagedPostgresUsageResponseGuardrailState:
    if value in MANAGED_POSTGRES_USAGE_RESPONSE_GUARDRAIL_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_RESPONSE_GUARDRAIL_STATE_VALUES!r}"
    )
