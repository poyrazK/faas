from typing import Literal

EnableManagedPostgresExtensionExtension = Literal["btree_gist", "pg_trgm", "vector"]

ENABLE_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES: set[EnableManagedPostgresExtensionExtension] = {
    "btree_gist",
    "pg_trgm",
    "vector",
}


def check_enable_managed_postgres_extension_extension(value: str) -> EnableManagedPostgresExtensionExtension:
    if value in ENABLE_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENABLE_MANAGED_POSTGRES_EXTENSION_EXTENSION_VALUES!r}"
    )
