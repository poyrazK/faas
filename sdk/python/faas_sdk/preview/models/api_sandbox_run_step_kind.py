from typing import Literal

APISandboxRunStepKind = Literal[
    "outbound_job",
    "outbound_webhook_attempt",
    "outbound_webhook_payload",
    "outbound_webhook_queued",
    "partner_webhook_outcome",
    "stripe_webhook",
    "webhook_duplicate",
    "webhook_processing",
]

API_SANDBOX_RUN_STEP_KIND_VALUES: set[APISandboxRunStepKind] = {
    "outbound_job",
    "outbound_webhook_attempt",
    "outbound_webhook_payload",
    "outbound_webhook_queued",
    "partner_webhook_outcome",
    "stripe_webhook",
    "webhook_duplicate",
    "webhook_processing",
}


def check_api_sandbox_run_step_kind(value: str) -> APISandboxRunStepKind:
    if value in API_SANDBOX_RUN_STEP_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RUN_STEP_KIND_VALUES!r}")
