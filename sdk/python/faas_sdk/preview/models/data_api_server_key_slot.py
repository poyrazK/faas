from typing import Literal

DataAPIServerKeySlot = Literal["a", "b"]

DATA_API_SERVER_KEY_SLOT_VALUES: set[DataAPIServerKeySlot] = {
    "a",
    "b",
}


def check_data_api_server_key_slot(value: str) -> DataAPIServerKeySlot:
    if value in DATA_API_SERVER_KEY_SLOT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_SERVER_KEY_SLOT_VALUES!r}")
