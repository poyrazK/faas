from typing import Literal

EdgeRuleSuggestionKind = Literal["cache", "cors", "headers", "jwt", "redirect", "rewrite", "throttle", "validate"]

EDGE_RULE_SUGGESTION_KIND_VALUES: set[EdgeRuleSuggestionKind] = {
    "cache",
    "cors",
    "headers",
    "jwt",
    "redirect",
    "rewrite",
    "throttle",
    "validate",
}


def check_edge_rule_suggestion_kind(value: str) -> EdgeRuleSuggestionKind:
    if value in EDGE_RULE_SUGGESTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_RULE_SUGGESTION_KIND_VALUES!r}")
