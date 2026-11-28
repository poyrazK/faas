from typing import Literal

DataAPIUsageGranularity = Literal["day", "hour"]

DATA_API_USAGE_GRANULARITY_VALUES: set[DataAPIUsageGranularity] = {
    "day",
    "hour",
}


def check_data_api_usage_granularity(value: str) -> DataAPIUsageGranularity:
    if value in DATA_API_USAGE_GRANULARITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_USAGE_GRANULARITY_VALUES!r}")
