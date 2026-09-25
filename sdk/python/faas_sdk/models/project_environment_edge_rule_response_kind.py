from typing import Literal

ProjectEnvironmentEdgeRuleResponseKind = Literal["cors", "headers", "ip", "redirect", "rewrite"]

PROJECT_ENVIRONMENT_EDGE_RULE_RESPONSE_KIND_VALUES: set[ProjectEnvironmentEdgeRuleResponseKind] = {
    "cors",
    "headers",
    "ip",
    "redirect",
    "rewrite",
}


def check_project_environment_edge_rule_response_kind(value: str) -> ProjectEnvironmentEdgeRuleResponseKind:
    if value in PROJECT_ENVIRONMENT_EDGE_RULE_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_EDGE_RULE_RESPONSE_KIND_VALUES!r}"
    )
