from typing import Literal

RouteAwakeSuggestionKind = Literal["call_less_often", "edge_cache"]

ROUTE_AWAKE_SUGGESTION_KIND_VALUES: set[RouteAwakeSuggestionKind] = {
    "call_less_often",
    "edge_cache",
}


def check_route_awake_suggestion_kind(value: str) -> RouteAwakeSuggestionKind:
    if value in ROUTE_AWAKE_SUGGESTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_AWAKE_SUGGESTION_KIND_VALUES!r}")
