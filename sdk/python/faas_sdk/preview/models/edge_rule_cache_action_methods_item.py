from typing import Literal

EdgeRuleCacheActionMethodsItem = Literal["GET", "HEAD"]

EDGE_RULE_CACHE_ACTION_METHODS_ITEM_VALUES: set[EdgeRuleCacheActionMethodsItem] = {
    "GET",
    "HEAD",
}


def check_edge_rule_cache_action_methods_item(value: str) -> EdgeRuleCacheActionMethodsItem:
    if value in EDGE_RULE_CACHE_ACTION_METHODS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_CACHE_ACTION_METHODS_ITEM_VALUES!r}")
