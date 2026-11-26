from typing import Literal

RouteSplitPhase = Literal["creating", "deploying", "done", "rolled_back", "rule_live", "undone"]

ROUTE_SPLIT_PHASE_VALUES: set[RouteSplitPhase] = {
    "creating",
    "deploying",
    "done",
    "rolled_back",
    "rule_live",
    "undone",
}


def check_route_split_phase(value: str) -> RouteSplitPhase:
    if value in ROUTE_SPLIT_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_SPLIT_PHASE_VALUES!r}")
