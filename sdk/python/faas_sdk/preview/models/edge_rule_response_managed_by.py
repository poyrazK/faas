from typing import Literal

EdgeRuleResponseManagedBy = Literal["attack_mode"]

EDGE_RULE_RESPONSE_MANAGED_BY_VALUES: set[EdgeRuleResponseManagedBy] = {
    "attack_mode",
}


def check_edge_rule_response_managed_by(value: str) -> EdgeRuleResponseManagedBy:
    if value in EDGE_RULE_RESPONSE_MANAGED_BY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_RESPONSE_MANAGED_BY_VALUES!r}")
