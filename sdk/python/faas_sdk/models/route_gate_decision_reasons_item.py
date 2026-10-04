from typing import Literal

RouteGateDecisionReasonsItem = Literal[
    "check_incomplete",
    "check_missing",
    "check_stale",
    "evidence_unavailable",
    "requirements_violated",
    "use_canary_advance",
    "verdict_unknown",
]

ROUTE_GATE_DECISION_REASONS_ITEM_VALUES: set[RouteGateDecisionReasonsItem] = {
    "check_incomplete",
    "check_missing",
    "check_stale",
    "evidence_unavailable",
    "requirements_violated",
    "use_canary_advance",
    "verdict_unknown",
}


def check_route_gate_decision_reasons_item(value: str) -> RouteGateDecisionReasonsItem:
    if value in ROUTE_GATE_DECISION_REASONS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_GATE_DECISION_REASONS_ITEM_VALUES!r}")
