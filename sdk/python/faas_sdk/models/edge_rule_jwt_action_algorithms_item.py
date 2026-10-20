from typing import Literal

EdgeRuleJWTActionAlgorithmsItem = Literal["ES256", "ES384", "ES512", "RS256", "RS384", "RS512"]

EDGE_RULE_JWT_ACTION_ALGORITHMS_ITEM_VALUES: set[EdgeRuleJWTActionAlgorithmsItem] = {
    "ES256",
    "ES384",
    "ES512",
    "RS256",
    "RS384",
    "RS512",
}


def check_edge_rule_jwt_action_algorithms_item(value: str) -> EdgeRuleJWTActionAlgorithmsItem:
    if value in EDGE_RULE_JWT_ACTION_ALGORITHMS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_JWT_ACTION_ALGORITHMS_ITEM_VALUES!r}")
