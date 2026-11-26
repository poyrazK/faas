from typing import Literal

PutManagedPostgresCustomRoleRequestDefaultForItem = Literal["read_only", "read_write"]

PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_DEFAULT_FOR_ITEM_VALUES: set[
    PutManagedPostgresCustomRoleRequestDefaultForItem
] = {
    "read_only",
    "read_write",
}


def check_put_managed_postgres_custom_role_request_default_for_item(
    value: str,
) -> PutManagedPostgresCustomRoleRequestDefaultForItem:
    if value in PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_DEFAULT_FOR_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PUT_MANAGED_POSTGRES_CUSTOM_ROLE_REQUEST_DEFAULT_FOR_ITEM_VALUES!r}"
    )
