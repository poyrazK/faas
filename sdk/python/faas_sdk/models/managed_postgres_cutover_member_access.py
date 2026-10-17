from typing import Literal

ManagedPostgresCutoverMemberAccess = Literal["data_api", "migration", "read_only", "read_write"]

MANAGED_POSTGRES_CUTOVER_MEMBER_ACCESS_VALUES: set[ManagedPostgresCutoverMemberAccess] = {
    "data_api",
    "migration",
    "read_only",
    "read_write",
}


def check_managed_postgres_cutover_member_access(value: str) -> ManagedPostgresCutoverMemberAccess:
    if value in MANAGED_POSTGRES_CUTOVER_MEMBER_ACCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_MEMBER_ACCESS_VALUES!r}")
