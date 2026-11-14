from typing import Literal

DataAPIChangeHookReportVersion = Literal[1]

DATA_API_CHANGE_HOOK_REPORT_VERSION_VALUES: set[DataAPIChangeHookReportVersion] = {
    1,
}


def check_data_api_change_hook_report_version(value: int) -> DataAPIChangeHookReportVersion:
    if value in DATA_API_CHANGE_HOOK_REPORT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CHANGE_HOOK_REPORT_VERSION_VALUES!r}")
