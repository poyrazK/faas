from typing import Literal

EdgeWAFReviewGroupRuleMode = Literal["block", "observe", "warn"]

EDGE_WAF_REVIEW_GROUP_RULE_MODE_VALUES: set[EdgeWAFReviewGroupRuleMode] = {
    "block",
    "observe",
    "warn",
}


def check_edge_waf_review_group_rule_mode(value: str) -> EdgeWAFReviewGroupRuleMode:
    if value in EDGE_WAF_REVIEW_GROUP_RULE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_WAF_REVIEW_GROUP_RULE_MODE_VALUES!r}")
