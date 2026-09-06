from typing import Literal

ManagedPostgresBindingAccess = Literal["read_only", "read_write"]

MANAGED_POSTGRES_BINDING_ACCESS_VALUES: set[ManagedPostgresBindingAccess] = {
    "read_only",
    "read_write",
}


def check_managed_postgres_binding_access(value: str) -> ManagedPostgresBindingAccess:
    if value in MANAGED_POSTGRES_BINDING_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_BINDING_ACCESS_VALUES!r}")
