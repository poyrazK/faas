from typing import Literal

PlatformTenantInvocationResponseState = Literal[
    "cancelled", "completed", "dead_letter", "dispatching", "failed", "pending"
]

PLATFORM_TENANT_INVOCATION_RESPONSE_STATE_VALUES: set[PlatformTenantInvocationResponseState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "dispatching",
    "failed",
    "pending",
}


def check_platform_tenant_invocation_response_state(value: str) -> PlatformTenantInvocationResponseState:
    if value in PLATFORM_TENANT_INVOCATION_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_INVOCATION_RESPONSE_STATE_VALUES!r}")
