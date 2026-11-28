from typing import Literal

DataConnectionCheckMode = Literal["snapshot"]

DATA_CONNECTION_CHECK_MODE_VALUES: set[DataConnectionCheckMode] = {
    "snapshot",
}


def check_data_connection_check_mode(value: str) -> DataConnectionCheckMode:
    if value in DATA_CONNECTION_CHECK_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_CONNECTION_CHECK_MODE_VALUES!r}")
