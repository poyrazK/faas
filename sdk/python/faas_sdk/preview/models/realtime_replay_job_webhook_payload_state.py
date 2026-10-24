from typing import Literal

RealtimeReplayJobWebhookPayloadState = Literal["cancelled", "completed", "running"]

REALTIME_REPLAY_JOB_WEBHOOK_PAYLOAD_STATE_VALUES: set[RealtimeReplayJobWebhookPayloadState] = {
    "cancelled",
    "completed",
    "running",
}


def check_realtime_replay_job_webhook_payload_state(value: str) -> RealtimeReplayJobWebhookPayloadState:
    if value in REALTIME_REPLAY_JOB_WEBHOOK_PAYLOAD_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_REPLAY_JOB_WEBHOOK_PAYLOAD_STATE_VALUES!r}")
