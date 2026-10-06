from typing import Literal

ManagedPostgresCapabilitiesCredentialAccessItem = Literal["migration", "read_only", "read_write"]

MANAGED_POSTGRES_CAPABILITIES_CREDENTIAL_ACCESS_ITEM_VALUES: set[ManagedPostgresCapabilitiesCredentialAccessItem] = {
    "migration",
    "read_only",
    "read_write",
}


def check_managed_postgres_capabilities_credential_access_item(
    value: str,
) -> ManagedPostgresCapabilitiesCredentialAccessItem:
    if value in MANAGED_POSTGRES_CAPABILITIES_CREDENTIAL_ACCESS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CAPABILITIES_CREDENTIAL_ACCESS_ITEM_VALUES!r}"
    )
