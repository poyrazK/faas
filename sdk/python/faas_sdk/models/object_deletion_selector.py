from typing import Literal

ObjectDeletionSelector = Literal["", "null"]

OBJECT_DELETION_SELECTOR_VALUES: set[ObjectDeletionSelector] = {
    "",
    "null",
}


def check_object_deletion_selector(value: str) -> ObjectDeletionSelector:
    if value in OBJECT_DELETION_SELECTOR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_DELETION_SELECTOR_VALUES!r}")
