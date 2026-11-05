from typing import Literal

RolloutAbortedWebhookPayloadRolloutState = Literal["aborted"]

ROLLOUT_ABORTED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES: set[RolloutAbortedWebhookPayloadRolloutState] = {
    "aborted",
}


def check_rollout_aborted_webhook_payload_rollout_state(value: str) -> RolloutAbortedWebhookPayloadRolloutState:
    if value in ROLLOUT_ABORTED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROLLOUT_ABORTED_WEBHOOK_PAYLOAD_ROLLOUT_STATE_VALUES!r}"
    )
