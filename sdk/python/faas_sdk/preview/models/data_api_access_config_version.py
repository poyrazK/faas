from typing import Literal

DataAPIAccessConfigVersion = Literal[1]

DATA_API_ACCESS_CONFIG_VERSION_VALUES: set[DataAPIAccessConfigVersion] = {
    1,
}


def check_data_api_access_config_version(value: int) -> DataAPIAccessConfigVersion:
    if value in DATA_API_ACCESS_CONFIG_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_ACCESS_CONFIG_VERSION_VALUES!r}")
