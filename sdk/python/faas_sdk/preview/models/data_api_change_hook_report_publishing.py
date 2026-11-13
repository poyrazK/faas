from typing import Literal

DataAPIChangeHookReportPublishing = Literal["ok", "retrying"]

DATA_API_CHANGE_HOOK_REPORT_PUBLISHING_VALUES: set[DataAPIChangeHookReportPublishing] = {
    "ok",
    "retrying",
}


def check_data_api_change_hook_report_publishing(value: str) -> DataAPIChangeHookReportPublishing:
    if value in DATA_API_CHANGE_HOOK_REPORT_PUBLISHING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CHANGE_HOOK_REPORT_PUBLISHING_VALUES!r}")
