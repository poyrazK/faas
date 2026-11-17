from typing import Literal

RequestDataConnectionCheckMode = Literal["snapshot"]

REQUEST_DATA_CONNECTION_CHECK_MODE_VALUES: set[RequestDataConnectionCheckMode] = {
    "snapshot",
}


def check_request_data_connection_check_mode(value: str) -> RequestDataConnectionCheckMode:
    if value in REQUEST_DATA_CONNECTION_CHECK_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_DATA_CONNECTION_CHECK_MODE_VALUES!r}")
