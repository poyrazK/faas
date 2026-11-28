from typing import Literal

EdgeRuleSuggestionMethodsItem = Literal["delete", "get", "head", "options", "patch", "post", "put", "trace"]

EDGE_RULE_SUGGESTION_METHODS_ITEM_VALUES: set[EdgeRuleSuggestionMethodsItem] = {
    "delete",
    "get",
    "head",
    "options",
    "patch",
    "post",
    "put",
    "trace",
}


def check_edge_rule_suggestion_methods_item(value: str) -> EdgeRuleSuggestionMethodsItem:
    if value in EDGE_RULE_SUGGESTION_METHODS_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_SUGGESTION_METHODS_ITEM_VALUES!r}")
