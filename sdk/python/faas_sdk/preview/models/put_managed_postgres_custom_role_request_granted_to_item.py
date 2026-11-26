from typing import Literal

PutManagedPostgresCustomRoleRequestGrantedToItem = Literal["migration", "read_only", "read_write", "session"]

PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_GRANTED_TO_ITEM_VALUES: set[
    PutManagedPostgresCustomRoleRequestGrantedToItem
] = {
    "migration",
    "read_only",
    "read_write",
    "session",
}


def check_put_managed_postgres_custom_role_request_granted_to_item(
    value: str,
) -> PutManagedPostgresCustomRoleRequestGrantedToItem:
    if value in PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_GRANTED_TO_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_GRANTED_TO_ITEM_VALUES!r}"
    )
