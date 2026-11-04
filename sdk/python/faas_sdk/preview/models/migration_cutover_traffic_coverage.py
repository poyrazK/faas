from typing import Literal

MigrationCutoverTrafficCoverage = Literal["deployment_route_observed_only"]

MIGRATION_CUTOVER_TRAFFIC_COVERAGE_VALUES: set[MigrationCutoverTrafficCoverage] = {
    "deployment_route_observed_only",
}


def check_migration_cutover_traffic_coverage(value: str) -> MigrationCutoverTrafficCoverage:
    if value in MIGRATION_CUTOVER_TRAFFIC_COVERAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIGRATION_CUTOVER_TRAFFIC_COVERAGE_VALUES!r}")
