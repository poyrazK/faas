from typing import Literal

DataAPIChangeEventBatchVersion = Literal[1]

DATA_API_CHANGE_EVENT_BATCH_VERSION_VALUES: set[DataAPIChangeEventBatchVersion] = {
    1,
}


def check_data_api_change_event_batch_version(value: int) -> DataAPIChangeEventBatchVersion:
    if value in DATA_API_CHANGE_EVENT_BATCH_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CHANGE_EVENT_BATCH_VERSION_VALUES!r}")
