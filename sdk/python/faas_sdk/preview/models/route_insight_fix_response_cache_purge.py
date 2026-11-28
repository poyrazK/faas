from typing import Literal

RouteInsightFixResponseCachePurge = Literal["failed", "requested"]

ROUTE_INSIGHT_FIX_RESPONSE_CACHE_PURGE_VALUES: set[RouteInsightFixResponseCachePurge] = {
    "failed",
    "requested",
}


def check_route_insight_fix_response_cache_purge(value: str) -> RouteInsightFixResponseCachePurge:
    if value in ROUTE_INSIGHT_FIX_RESPONSE_CACHE_PURGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_INSIGHT_FIX_RESPONSE_CACHE_PURGE_VALUES!r}")
