from typing import Literal

ManagedPostgresCapabilitiesServiceClassesItem = Literal["burstable", "development", "production"]

MANAGED_POSTGRES_CAPABILITIES_SERVICE_CLASSES_ITEM_VALUES: set[ManagedPostgresCapabilitiesServiceClassesItem] = {
    "burstable",
    "development",
    "production",
}


def check_managed_postgres_capabilities_service_classes_item(
    value: str,
) -> ManagedPostgresCapabilitiesServiceClassesItem:
    if value in MANAGED_POSTGRES_CAPABILITIES_SERVICE_CLASSES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CAPABILITIES_SERVICE_CLASSES_ITEM_VALUES!r}"
    )
