from typing import Literal

CreateObjectStorageComputeBindingRequestPermission = Literal["read", "read_write", "write"]

CREATE_OBJECT_STORAGE_COMPUTE_BINDING_REQUEST_PERMISSION_VALUES: set[
    CreateObjectStorageComputeBindingRequestPermission
] = {
    "read",
    "read_write",
    "write",
}


def check_create_object_storage_compute_binding_request_permission(
    value: str,
) -> CreateObjectStorageComputeBindingRequestPermission:
    if value in CREATE_OBJECT_STORAGE_COMPUTE_BINDING_REQUEST_PERMISSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_OBJECT_STORAGE_COMPUTE_BINDING_REQUEST_PERMISSION_VALUES!r}"
    )
