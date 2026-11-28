from typing import Literal

RouteMonitorIncidentTimelineEntryCoverage = Literal["observed_only"]

ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_COVERAGE_VALUES: set[RouteMonitorIncidentTimelineEntryCoverage] = {
    "observed_only",
}


def check_route_monitor_incident_timeline_entry_coverage(value: str) -> RouteMonitorIncidentTimelineEntryCoverage:
    if value in ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_COVERAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROUTE_MONITOR_INCIDENT_TIMELINE_ENTRY_COVERAGE_VALUES!r}"
    )
