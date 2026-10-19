from typing import Literal

MigrationCutoverTrafficStatus = Literal["healthy", "unknown", "violated"]

MIGRATION_CUTOVER_TRAFFIC_STATUS_VALUES: set[MigrationCutoverTrafficStatus] = {
    "healthy",
    "unknown",
    "violated",
}


def check_migration_cutover_traffic_status(value: str) -> MigrationCutoverTrafficStatus:
    if value in MIGRATION_CUTOVER_TRAFFIC_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_CUTOVER_TRAFFIC_STATUS_VALUES!r}")
