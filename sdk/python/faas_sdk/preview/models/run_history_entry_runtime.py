from typing import Literal

RunHistoryEntryRuntime = Literal["node22", "node24", "python312", "python313"]

RUN_HISTORY_ENTRY_RUNTIME_VALUES: set[RunHistoryEntryRuntime] = {
    "node22",
    "node24",
    "python312",
    "python313",
}


def check_run_history_entry_runtime(value: str) -> RunHistoryEntryRuntime:
    if value in RUN_HISTORY_ENTRY_RUNTIME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_HISTORY_ENTRY_RUNTIME_VALUES!r}")
