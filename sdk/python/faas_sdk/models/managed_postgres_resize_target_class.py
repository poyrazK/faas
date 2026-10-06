from typing import Literal

ManagedPostgresResizeTargetClass = Literal["burstable", "development", "production"]

MANAGED_POSTGRES_RESIZE_TARGET_CLASS_VALUES: set[ManagedPostgresResizeTargetClass] = {
    "burstable",
    "development",
    "production",
}


def check_managed_postgres_resize_target_class(value: str) -> ManagedPostgresResizeTargetClass:
    if value in MANAGED_POSTGRES_RESIZE_TARGET_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_RESIZE_TARGET_CLASS_VALUES!r}")
