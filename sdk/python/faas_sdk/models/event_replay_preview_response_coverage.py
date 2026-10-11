from typing import Literal

EventReplayPreviewResponseCoverage = Literal["retained_envelopes"]

EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES: set[EventReplayPreviewResponseCoverage] = {
    "retained_envelopes",
}


def check_event_replay_preview_response_coverage(value: str) -> EventReplayPreviewResponseCoverage:
    if value in EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES!r}")
