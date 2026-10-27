from typing import Literal

DevBridgeWebhookReplayState = Literal["completed", "dispatching", "uncertain"]

DEV_BRIDGE_WEBHOOK_REPLAY_STATE_VALUES: set[DevBridgeWebhookReplayState] = {
    "completed",
    "dispatching",
    "uncertain",
}


def check_dev_bridge_webhook_replay_state(value: str) -> DevBridgeWebhookReplayState:
    if value in DEV_BRIDGE_WEBHOOK_REPLAY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_BRIDGE_WEBHOOK_REPLAY_STATE_VALUES!r}")
