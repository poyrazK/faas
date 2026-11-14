from typing import Literal

DataAPIChangeHookStatusPublishing = Literal["ok", "retrying"]

DATA_API_CHANGE_HOOK_STATUS_PUBLISHING_VALUES: set[DataAPIChangeHookStatusPublishing] = {
    "ok",
    "retrying",
}


def check_data_api_change_hook_status_publishing(value: str) -> DataAPIChangeHookStatusPublishing:
    if value in DATA_API_CHANGE_HOOK_STATUS_PUBLISHING_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_CHANGE_HOOK_STATUS_PUBLISHING_VALUES!r}")
