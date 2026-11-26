from typing import Literal

EnableOrgManagedPostgresExtensionExtension = Literal["btree_gist", "pg_trgm", "vector"]

ENABLE_ORG_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES: set[EnableOrgManagedPostgresExtensionExtension] = {
    "btree_gist",
    "pg_trgm",
    "vector",
}


def check_enable_org_managed_postgres_extension_extension(value: str) -> EnableOrgManagedPostgresExtensionExtension:
    if value in ENABLE_ORG_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENABLE_ORG_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES!r}"
    )
