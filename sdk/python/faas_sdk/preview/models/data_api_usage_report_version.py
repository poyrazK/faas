from typing import Literal

DataAPIUsageReportVersion = Literal[1]

DATA_API_USAGE_REPORT_VERSION_VALUES: set[DataAPIUsageReportVersion] = {
    1,
}


def check_data_api_usage_report_version(value: int) -> DataAPIUsageReportVersion:
    if value in DATA_API_USAGE_REPORT_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_USAGE_REPORT_VERSION_VALUES!r}")
