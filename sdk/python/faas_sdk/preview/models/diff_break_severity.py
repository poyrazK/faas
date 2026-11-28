from typing import Literal

DiffBreakSeverity = Literal["error", "warn"]

DIFF_BREAK_SEVERITY_VALUES: set[DiffBreakSeverity] = {
    "error",
    "warn",
}


def check_diff_break_severity(value: str) -> DiffBreakSeverity:
    if value in DIFF_BREAK_SEVERITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_BREAK_SEVERITY_VALUES!r}")
