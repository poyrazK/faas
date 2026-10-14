from typing import Literal

ListMCPAdmissionHistoryOutcome = Literal["accepted", "rejected"]

LIST_MCP_ADMISSION_HISTORY_OUTCOME_VALUES: set[ListMCPAdmissionHistoryOutcome] = {
    "accepted",
    "rejected",
}


def check_list_mcp_admission_history_outcome(value: str) -> ListMCPAdmissionHistoryOutcome:
    if value in LIST_MCP_ADMISSION_HISTORY_OUTCOME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_MCP_ADMISSION_HISTORY_OUTCOME_VALUES!r}")
