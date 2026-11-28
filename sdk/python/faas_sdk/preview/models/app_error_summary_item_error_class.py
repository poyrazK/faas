from typing import Literal

AppErrorSummaryItemErrorClass = Literal[
    "client_error",
    "db_timeout",
    "invalid_json",
    "null_pointer",
    "stripe_timeout",
    "unhandled",
    "upstream_5xx",
    "wake_failed",
]

APP_ERROR_SUMMARY_ITEM_ERROR_CLASS_VALUES: set[AppErrorSummaryItemErrorClass] = {
    "client_error",
    "db_timeout",
    "invalid_json",
    "null_pointer",
    "stripe_timeout",
    "unhandled",
    "upstream_5xx",
    "wake_failed",
}


def check_app_error_summary_item_error_class(value: str) -> AppErrorSummaryItemErrorClass:
    if value in APP_ERROR_SUMMARY_ITEM_ERROR_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_ERROR_SUMMARY_ITEM_ERROR_CLASS_VALUES!r}")
