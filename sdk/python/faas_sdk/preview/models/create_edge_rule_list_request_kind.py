from typing import Literal

CreateEdgeRuleListRequestKind = Literal["asn", "country", "host", "ip", "string"]

CREATE_EDGE_RULE_LIST_REQUEST_KIND_VALUES: set[CreateEdgeRuleListRequestKind] = {
    "asn",
    "country",
    "host",
    "ip",
    "string",
}


def check_create_edge_rule_list_request_kind(value: str) -> CreateEdgeRuleListRequestKind:
    if value in CREATE_EDGE_RULE_LIST_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_EDGE_RULE_LIST_REQUEST_KIND_VALUES!r}")
