from typing import Literal

BindingVerificationObjectStorageMode = Literal["read", "read_write", "write"]

BINDING_VERIFICATION_OBJECT_STORAGE_MODE_VALUES: set[BindingVerificationObjectStorageMode] = {
    "read",
    "read_write",
    "write",
}


def check_binding_verification_object_storage_mode(value: str) -> BindingVerificationObjectStorageMode:
    if value in BINDING_VERIFICATION_OBJECT_STORAGE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BINDING_VERIFICATION_OBJECT_STORAGE_MODE_VALUES!r}")
