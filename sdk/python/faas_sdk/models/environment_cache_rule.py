from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.edge_rule_cache_action import EdgeRuleCacheAction


T = TypeVar("T", bound="EnvironmentCacheRule")


@_attrs_define
class EnvironmentCacheRule:
    """An unconditional cache rule owned by exactly one immutable named environment host."""

    path: str
    """Concrete cache path glob; OpenAPI templates and backslashes are rejected."""
    priority: int
    """Must be distinct within this replacement."""
    cache: EdgeRuleCacheAction
    """Edge-cache control primitive (ADR-122, kind=cache). Wraps the
    response body in a per-route freshness window so a wake-elision
    cache absorbs repeat traffic without paying a cold-boot cost.

    Field-by-field:
      * `max_age_seconds` — required. Fresh window in seconds. The
        apid-side default is 60; the absolute platform cap is 3600
        (`api.ResponseCacheMaxAgeMaxSeconds`). The runtime cache
        layer in `pkg/gateway/response_cache.go` re-checks this
        cap as defence-in-depth.
      * `stale_while_revalidate_seconds` — optional. After the fresh
        window, return the cached response immediately while one
        gateway request refreshes the key in the background. Default
        0 (disabled); absolute cap 300.
      * `stale_if_error_seconds` — required. Stale-on-error window
        in seconds. Default 300; absolute cap 300
        (`api.ResponseCacheStaleIfErrorMaxSeconds`). During an
        upstream error the gateway returns the cached body within
        this window instead of 502/504.
      * `vary_on` — optional closed vocabulary of non-credential
        request headers that participate in the cache key.
        Allowed values: `Accept-Language`, `Accept-Encoding`.
        Credential-bearing headers (Authorization, Cookie) are
        deliberately excluded — authed requests bypass the cache
        entirely (ADR-122 D3).
      * `methods` — optional closed vocabulary of HTTP methods
        eligible for caching. Allowed values: `GET`, `HEAD`.
        POST/PUT/PATCH/DELETE are deliberately excluded —
        caching their responses is either incorrect (idempotency
        breaks under retry) or unsafe (cross-user state).
    """

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        priority = self.priority

        cache = self.cache.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
                "priority": priority,
                "cache": cache,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.edge_rule_cache_action import EdgeRuleCacheAction

        d = dict(src_dict)
        path = d.pop("path")

        priority = d.pop("priority")

        cache = EdgeRuleCacheAction.from_dict(d.pop("cache"))

        environment_cache_rule = cls(
            path=path,
            priority=priority,
            cache=cache,
        )

        return environment_cache_rule
