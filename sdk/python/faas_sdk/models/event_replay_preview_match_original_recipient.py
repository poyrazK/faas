from typing import Literal

EventReplayPreviewMatchOriginalRecipient = Literal["captured", "not_captured", "unknown"]

EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES: set[EventReplayPreviewMatchOriginalRecipient] = {
    "captured",
    "not_captured",
    "unknown",
}


def check_event_replay_preview_match_original_recipient(value: str) -> EventReplayPreviewMatchOriginalRecipient:
    if value in EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_PREVIEW_MATCH_ORIGINAL_RECIPIENT_VALUES!r}"
    )
