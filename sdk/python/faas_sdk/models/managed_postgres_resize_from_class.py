from typing import Literal

ManagedPostgresResizeFromClass = Literal["burstable", "development", "production"]

MANAGED_POSTGRES_RESIZE_FROM_CLASS_VALUES: set[ManagedPostgresResizeFromClass] = {
    "burstable",
    "development",
    "production",
}


def check_managed_postgres_resize_from_class(value: str) -> ManagedPostgresResizeFromClass:
    if value in MANAGED_POSTGRES_RESIZE_FROM_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RESIZE_FROM_CLASS_VALUES!r}")
