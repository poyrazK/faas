from typing import Literal

APISandboxRunStepInvocationState = Literal[
    "cancelled", "completed", "dead_letter", "dispatching", "expired", "failed", "pending", "superseded"
]

API_SANDBOX_RUN_STEP_INVOCATION_STATE_VALUES: set[APISandboxRunStepInvocationState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "dispatching",
    "expired",
    "failed",
    "pending",
    "superseded",
}


def check_api_sandbox_run_step_invocation_state(value: str) -> APISandboxRunStepInvocationState:
    if value in API_SANDBOX_RUN_STEP_INVOCATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RUN_STEP_INVOCATION_STATE_VALUES!r}")
