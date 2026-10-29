from typing import Literal

RolloutCompletedWebhookPayloadRolloutState = Literal["complete"]

ROLLOUT_COMPLETED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES: set[RolloutCompletedWebhookPayloadRolloutState] = {
    "complete",
}


def check_rollout_completed_webhook_payload_rollout_state(value: str) -> RolloutCompletedWebhookPayloadRolloutState:
    if value in ROLLOUT_COMPLETED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROLLOUT_COMPLETED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES!r}"
    )
