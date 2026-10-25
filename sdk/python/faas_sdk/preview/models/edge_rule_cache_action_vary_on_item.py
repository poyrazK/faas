from typing import Literal

EdgeRuleCacheActionVaryOnItem = Literal["Accept-Encoding", "Accept-Language"]

EDGE_RULE_CACHE_ACTION_VARY_ON_ITEM_VALUES: set[EdgeRuleCacheActionVaryOnItem] = {
    "Accept-Encoding",
    "Accept-Language",
}


def check_edge_rule_cache_action_vary_on_item(value: str) -> EdgeRuleCacheActionVaryOnItem:
    if value in EDGE_RULE_CACHE_ACTION_VARY_ON_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_CACHE_ACTION_VARY_ON_ITEM_VALUES!r}")
