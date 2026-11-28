from typing import Literal

GetRuntimePolicyStatusEdgeRulesScope = Literal["account", "app", "fleet"]

GET_RUNTIME_POLICY_STATUS_EDGE_RULES_SCOPE_VALUES: set[GetRuntimePolicyStatusEdgeRulesScope] = {
    "account",
    "app",
    "fleet",
}


def check_get_runtime_policy_status_edge_rules_scope(value: str) -> GetRuntimePolicyStatusEdgeRulesScope:
    if value in GET_RUNTIME_POLICY_STATUS_EDGE_RULES_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_RUNTIME_POLICY_STATUS_EDGE_RULES_SCOPE_VALUES!r}"
    )
