from typing import Literal

DataAPIServerKeyPermissionSetAccess = Literal["read", "write"]

DATA_API_SERVER_KEY_PERMISSION_SET_ACCESS_VALUES: set[DataAPIServerKeyPermissionSetAccess] = {
    "read",
    "write",
}


def check_data_api_server_key_permission_set_access(value: str) -> DataAPIServerKeyPermissionSetAccess:
    if value in DATA_API_SERVER_KEY_PERMISSION_SET_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DATA_API_SERVER_KEY_PERMISSION_SET_ACCESS_VALUES!r}")
