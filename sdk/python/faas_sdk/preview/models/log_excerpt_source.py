from typing import Literal

LogExcerptSource = Literal["app", "build", "gateway", "vm-init"]

LOG_EXCERPT_SOURCE_VALUES: set[LogExcerptSource] = {
    "app",
    "build",
    "gateway",
    "vm-init",
}


def check_log_excerpt_source(value: str) -> LogExcerptSource:
    if value in LOG_EXCERPT_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LOG_EXCERPT_SOURCE_VALUES!r}")
