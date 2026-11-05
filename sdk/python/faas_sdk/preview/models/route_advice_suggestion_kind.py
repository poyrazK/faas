from typing import Literal

RouteAdviceSuggestionKind = Literal["async", "cache", "priority", "throttle", "wake_ahead"]

ROUTE_ADVICE_SUGGESTION_KIND_VALUES: set[RouteAdviceSuggestionKind] = {
    "async",
    "cache",
    "priority",
    "throttle",
    "wake_ahead",
}


def check_route_advice_suggestion_kind(value: str) -> RouteAdviceSuggestionKind:
    if value in ROUTE_ADVICE_SUGGESTION_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_ADVICE_SUGGESTION_KIND_VALUES!r}")
