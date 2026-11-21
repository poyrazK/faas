from typing import Literal

RunHistoryEntryResultValidationStatus = Literal["invalid", "valid"]

RUN_HISTORY_ENTRY_RESULT_VALIDATION_STATUS_VALUES: set[RunHistoryEntryResultValidationStatus] = {
    "invalid",
    "valid",
}


def check_run_history_entry_result_validation_status(value: str) -> RunHistoryEntryResultValidationStatus:
    if value in RUN_HISTORY_ENTRY_RESULT_VALIDATION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RUN_HISTORY_ENTRY_RESULT_VALIDATION_STATUS_VALUES!r}"
    )
