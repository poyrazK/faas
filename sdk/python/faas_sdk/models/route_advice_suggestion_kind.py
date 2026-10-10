from typing import Literal

RouteAdviceSuggestionKind = Literal["async", "cache", "throttle"]

ROUTE_ADVICE_SUGGESTION_KIND_VALUES: set[RouteAdviceSuggestionKind] = {
    "async",
    "cache",
    "throttle",
}


def check_route_advice_suggestion_kind(value: str) -> RouteAdviceSuggestionKind:
    if value in ROUTE_ADVICE_SUGGESTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_SUGGESTION_KIND_VALUES!r}")
