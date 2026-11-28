from typing import Literal

EdgeRuleResponseUnreleasedFeaturesItem = Literal[
    "ai_crawlers", "attack_mode", "challenge", "penalty_box", "waf_blocking"
]

EDGE_RULE_RESPONSE_UNRELEASED_FEATURES_ITEM_VALUES: set[EdgeRuleResponseUnreleasedFeaturesItem] = {
    "ai_crawlers",
    "attack_mode",
    "challenge",
    "penalty_box",
    "waf_blocking",
}


def check_edge_rule_response_unreleased_features_item(value: str) -> EdgeRuleResponseUnreleasedFeaturesItem:
    if value in EDGE_RULE_RESPONSE_UNRELEASED_FEATURES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EDGE_RULE_RESPONSE_UNRELEASED_FEATURES_ITEM_VALUES!r}"
    )
