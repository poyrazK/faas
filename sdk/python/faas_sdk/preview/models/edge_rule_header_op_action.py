from typing import Literal

EdgeRuleHeaderOpAction = Literal["add", "remove", "set"]

EDGE_RULE_HEADER_OP_ACTION_VALUES: set[EdgeRuleHeaderOpAction] = {
    "add",
    "remove",
    "set",
}


def check_edge_rule_header_op_action(value: str) -> EdgeRuleHeaderOpAction:
    if value in EDGE_RULE_HEADER_OP_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_HEADER_OP_ACTION_VALUES!r}")
