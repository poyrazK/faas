from typing import Literal

ManagedPostgresUsageImportReadingMeter = Literal[
    "active_seconds",
    "compute_unit_seconds",
    "egress_bytes",
    "history_byte_seconds",
    "operations",
    "storage_byte_seconds",
]

MANAGED_POSTGRES_USAGE_IMPORT_READING_METER_VALUES: set[ManagedPostgresUsageImportReadingMeter] = {
    "active_seconds",
    "compute_unit_seconds",
    "egress_bytes",
    "history_byte_seconds",
    "operations",
    "storage_byte_seconds",
}


def check_managed_postgres_usage_import_reading_meter(value: str) -> ManagedPostgresUsageImportReadingMeter:
    if value in MANAGED_POSTGRES_USAGE_IMPORT_READING_METER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_USAGE_IMPORT_READING_METER_VALUES!r}"
    )
