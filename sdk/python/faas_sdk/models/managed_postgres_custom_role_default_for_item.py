from typing import Literal

ManagedPostgresCustomRoleDefaultForItem = Literal["read_only", "read_write"]

MANAGED_POSTGRES_CUSTOM_ROLE_DEFAULT_FOR_ITEM_VALUES: set[ManagedPostgresCustomRoleDefaultForItem] = {
    "read_only",
    "read_write",
}


def check_managed_postgres_custom_role_default_for_item(value: str) -> ManagedPostgresCustomRoleDefaultForItem:
    if value in MANAGED_POSTGRES_CUSTOM_ROLE_DEFAULT_FOR_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUSTOM_ROLE_DEFAULT_FOR_ITEM_VALUES!r}"
    )
