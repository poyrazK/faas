from typing import Literal

RouteFindingChangeKind = Literal["changed", "newly_violated", "observed", "removed", "resolved", "unknown"]

ROUTE_FINDING_CHANGE_KIND_VALUES: set[RouteFindingChangeKind] = {
    "changed",
    "newly_violated",
    "observed",
    "removed",
    "resolved",
    "unknown",
}


def check_route_finding_change_kind(value: str) -> RouteFindingChangeKind:
    if value in ROUTE_FINDING_CHANGE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_FINDING_CHANGE_KIND_VALUES!r}")
