from typing import Literal

ManagedPostgresCapabilitiesAvailabilityItem = Literal["high_availability", "single_zone"]

MANAGED_POSTGRES_CAPABILITIES_AVAILABILITY_ITEM_VALUES: set[ManagedPostgresCapabilitiesAvailabilityItem] = {
    "high_availability",
    "single_zone",
}


def check_managed_postgres_capabilities_availability_item(value: str) -> ManagedPostgresCapabilitiesAvailabilityItem:
    if value in MANAGED_POSTGRES_CAPABILITIES_AVAILABILITY_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CAPABILITIES_AVAILABILITY_ITEM_VALUES!r}"
    )
