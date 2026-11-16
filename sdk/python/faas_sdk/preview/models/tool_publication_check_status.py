from typing import Literal

ToolPublicationCheckStatus = Literal["passed"]

TOOL_PUBLICATION_CHECK_STATUS_VALUES: set[ToolPublicationCheckStatus] = {
    "passed",
}


def check_tool_publication_check_status(value: str) -> ToolPublicationCheckStatus:
    if value in TOOL_PUBLICATION_CHECK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_CHECK_STATUS_VALUES!r}")
