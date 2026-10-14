from typing import Literal

BindingCheckBindingResultRequiredObjectStorageMode = Literal["read", "read_write", "write"]

BINDING_CHECK_BINDING_RESULT_REQUIRED_OBJECT_STORAGE_MODE_VALUES: set[
    BindingCheckBindingResultRequiredObjectStorageMode
] = {
    "read",
    "read_write",
    "write",
}


def check_binding_check_binding_result_required_object_storage_mode(
    value: str,
) -> BindingCheckBindingResultRequiredObjectStorageMode:
    if value in BINDING_CHECK_BINDING_RESULT_REQUIRED_OBJECT_STORAGE_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {BINDING_CHECK_BINDING_RESULT_REQUIRED_OBJECT_STORAGE_MODE_VALUES!r}"
    )
