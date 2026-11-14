from typing import Literal

ToolPublicationOutcomeStatus = Literal["completed", "failed", "outcome_unknown"]

TOOL_PUBLICATION_OUTCOME_STATUS_VALUES: set[ToolPublicationOutcomeStatus] = {
    "completed",
    "failed",
    "outcome_unknown",
}


def check_tool_publication_outcome_status(value: str) -> ToolPublicationOutcomeStatus:
    if value in TOOL_PUBLICATION_OUTCOME_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_OUTCOME_STATUS_VALUES!r}")
