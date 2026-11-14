from typing import Literal

ToolPublicationRecordStatus = Literal["completed", "failed", "outcome_unknown", "running"]

TOOL_PUBLICATION_RECORD_STATUS_VALUES: set[ToolPublicationRecordStatus] = {
    "completed",
    "failed",
    "outcome_unknown",
    "running",
}


def check_tool_publication_record_status(value: str) -> ToolPublicationRecordStatus:
    if value in TOOL_PUBLICATION_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_RECORD_STATUS_VALUES!r}")
