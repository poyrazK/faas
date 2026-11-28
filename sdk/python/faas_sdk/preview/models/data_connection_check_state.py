from typing import Literal

DataConnectionCheckState = Literal["blocked", "passed", "pending", "running", "stale"]

DATA_CONNECTION_CHECK_STATE_VALUES: set[DataConnectionCheckState] = {
    "blocked",
    "passed",
    "pending",
    "running",
    "stale",
}


def check_data_connection_check_state(value: str) -> DataConnectionCheckState:
    if value in DATA_CONNECTION_CHECK_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_CONNECTION_CHECK_STATE_VALUES!r}")
