from typing import Literal

EdgeRuleListResponseKind = Literal["country", "host", "ip", "string"]

EDGE_RULE_LIST_RESPONSE_KIND_VALUES: set[EdgeRuleListResponseKind] = {
    "country",
    "host",
    "ip",
    "string",
}


def check_edge_rule_list_response_kind(value: str) -> EdgeRuleListResponseKind:
    if value in EDGE_RULE_LIST_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_LIST_RESPONSE_KIND_VALUES!r}")
