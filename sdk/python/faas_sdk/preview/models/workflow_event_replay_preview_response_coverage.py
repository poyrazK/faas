from typing import Literal

WorkflowEventReplayPreviewResponseCoverage = Literal["retained_envelopes"]

WORKFLOW_EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES: set[WorkflowEventReplayPreviewResponseCoverage] = {
    "retained_envelopes",
}


def check_workflow_event_replay_preview_response_coverage(value: str) -> WorkflowEventReplayPreviewResponseCoverage:
    if value in WORKFLOW_EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WORKFLOW_EVENT_REPLAY_PREVIEW_RESPONSE_COVERAGE_VALUES!r}"
    )
