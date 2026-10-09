from typing import Literal

EdgeRuleMatchExprOp = Literal[
    "cidr", "contains", "eq", "exists", "in", "in_list", "missing", "ne", "not_in", "prefix", "regex", "suffix"
]

EDGE_RULE_MATCH_EXPR_OP_VALUES: set[EdgeRuleMatchExprOp] = {
    "cidr",
    "contains",
    "eq",
    "exists",
    "in",
    "in_list",
    "missing",
    "ne",
    "not_in",
    "prefix",
    "regex",
    "suffix",
}


def check_edge_rule_match_expr_op(value: str) -> EdgeRuleMatchExprOp:
    if value in EDGE_RULE_MATCH_EXPR_OP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_MATCH_EXPR_OP_VALUES!r}")
