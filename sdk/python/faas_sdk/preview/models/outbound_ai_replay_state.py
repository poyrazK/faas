from typing import Literal

OutboundAIReplayState = Literal["done", "failed", "pending", "running"]

OUTBOUND_AI_REPLAY_STATE_VALUES: set[OutboundAIReplayState] = {
    "done",
    "failed",
    "pending",
    "running",
}


def check_outbound_ai_replay_state(value: str) -> OutboundAIReplayState:
    if value in OUTBOUND_AI_REPLAY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OUTBOUND_AI_REPLAY_STATE_VALUES!r}")
