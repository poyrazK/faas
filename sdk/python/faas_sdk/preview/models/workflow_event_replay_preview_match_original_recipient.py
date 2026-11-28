from typing import Literal

WorkflowEventReplayPreviewMatchOriginalRecipient = Literal["captured"]

WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES: set[WorkflowEventReplayPreviewMatchOriginalRecipient] = {
    "captured",
}


def check_workflow_event_replay_preview_match_original_recipient(
    value: str,
) -> WorkflowEventReplayPreviewMatchOriginalRecipient:
    if value in WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES!r}"
    )
