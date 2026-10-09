from typing import Literal

WorkflowEventReplayPreviewMatchRoutingState = Literal["enqueued", "failed", "filtered", "pending", "processing"]

WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ROUTING_STATE_VALUES: set[WorkflowEventReplayPreviewMatchRoutingState] = {
    "enqueued",
    "failed",
    "filtered",
    "pending",
    "processing",
}


def check_workflow_event_replay_preview_match_routing_state(value: str) -> WorkflowEventReplayPreviewMatchRoutingState:
    if value in WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ROUTING_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ROUTING_STATE_VALUES!r}"
    )
