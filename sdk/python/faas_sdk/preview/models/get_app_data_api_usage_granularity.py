from typing import Literal

GetAppDataAPIUsageGranularity = Literal["day", "hour"]

GET_APP_DATA_API_USAGE_GRANULARITY_VALUES: set[GetAppDataAPIUsageGranularity] = {
    "day",
    "hour",
}


def check_get_app_data_api_usage_granularity(value: str) -> GetAppDataAPIUsageGranularity:
    if value in GET_APP_DATA_API_USAGE_GRANULARITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_DATA_API_USAGE_GRANULARITY_VALUES!r}")
