from typing import Literal

SyntheticCheckRunErrorClass = Literal["connect", "dns", "other", "status", "timeout", "tls"]

SYNTHETIC_CHECK_RUN_ERROR_CLASS_VALUES: set[SyntheticCheckRunErrorClass] = {
    "connect",
    "dns",
    "other",
    "status",
    "timeout",
    "tls",
}


def check_synthetic_check_run_error_class(value: str) -> SyntheticCheckRunErrorClass:
    if value in SYNTHETIC_CHECK_RUN_ERROR_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SYNTHETIC_CHECK_RUN_ERROR_CLASS_VALUES!r}")
