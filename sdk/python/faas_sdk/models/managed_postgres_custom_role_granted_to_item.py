from typing import Literal

ManagedPostgresCustomRoleGrantedToItem = Literal["migration", "read_only", "read_write"]

MANAGED_POSTGRES_CUSTOM_ROLE_GRANTED_TO_ITEM_VALUES: set[ManagedPostgresCustomRoleGrantedToItem] = {
    "migration",
    "read_only",
    "read_write",
}


def check_managed_postgres_custom_role_granted_to_item(value: str) -> ManagedPostgresCustomRoleGrantedToItem:
    if value in MANAGED_POSTGRES_CUSTOM_ROLE_GRANTED_TO_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUSTOM_ROLE_GRANTED_TO_ITEM_VALUES!r}"
    )
