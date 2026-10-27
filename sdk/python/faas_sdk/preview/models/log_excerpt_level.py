from typing import Literal

LogExcerptLevel = Literal["error", "info", "warn"]

LOG_EXCERPT_LEVEL_VALUES: set[LogExcerptLevel] = {
    "error",
    "info",
    "warn",
}


def check_log_excerpt_level(value: str) -> LogExcerptLevel:
    if value in LOG_EXCERPT_LEVEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LOG_EXCERPT_LEVEL_VALUES!r}")
