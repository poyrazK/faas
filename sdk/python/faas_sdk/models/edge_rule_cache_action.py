from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.edge_rule_cache_action_methods_item import (
    EdgeRuleCacheActionMethodsItem,
    check_edge_rule_cache_action_methods_item,
)
from ..models.edge_rule_cache_action_vary_on_item import (
    EdgeRuleCacheActionVaryOnItem,
    check_edge_rule_cache_action_vary_on_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleCacheAction")


@_attrs_define
class EdgeRuleCacheAction:
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

    max_age_seconds: int = 60
    """Fresh window in seconds. 0 = inherit the apid-side default
    (60s). Positive values are clamped to the platform cap
    (3600s = 1 hour).
    """
    stale_if_error_seconds: int = 300
    """Stale-on-error window in seconds. 0 = no stale-on-error
    (errors return 502/504 directly). Positive values are
    clamped to the platform cap (300s = 5 minutes).
    """
    stale_while_revalidate_seconds: int | Unset = 0
    """After the fresh window, serve stale for this many seconds
    while a single coalesced background request refreshes the
    cache key. 0 disables stale-while-revalidate.
    """
    vary_on: list[EdgeRuleCacheActionVaryOnItem] | Unset = UNSET
    """Non-credential request headers that participate in the
    cache key. Closed vocabulary:
      - `Accept-Language`
      - `Accept-Encoding`
    Empty array (default) collapses to no extra key
    dimensions; the URL path + query alone drives cache
    identity.
    """
    methods: list[EdgeRuleCacheActionMethodsItem] | Unset = UNSET
    """HTTP methods eligible for caching. Closed vocabulary:
      - `GET`
      - `HEAD`
    Empty array (default) collapses to the runtime's
    cacheability predicate (GET + HEAD). POST/PUT/PATCH/DELETE
    are deliberately absent.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_age_seconds = self.max_age_seconds

        stale_if_error_seconds = self.stale_if_error_seconds

        stale_while_revalidate_seconds = self.stale_while_revalidate_seconds

        vary_on: list[str] | Unset = UNSET
        if not isinstance(self.vary_on, Unset):
            vary_on = []
            for vary_on_item_data in self.vary_on:
                vary_on_item: str = vary_on_item_data
                vary_on.append(vary_on_item)

        methods: list[str] | Unset = UNSET
        if not isinstance(self.methods, Unset):
            methods = []
            for methods_item_data in self.methods:
                methods_item: str = methods_item_data
                methods.append(methods_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_age_seconds": max_age_seconds,
                "stale_if_error_seconds": stale_if_error_seconds,
            }
        )
        if stale_while_revalidate_seconds is not UNSET:
            field_dict["stale_while_revalidate_seconds"] = stale_while_revalidate_seconds
        if vary_on is not UNSET:
            field_dict["vary_on"] = vary_on
        if methods is not UNSET:
            field_dict["methods"] = methods

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_age_seconds = d.pop("max_age_seconds")

        stale_if_error_seconds = d.pop("stale_if_error_seconds")

        stale_while_revalidate_seconds = d.pop("stale_while_revalidate_seconds", UNSET)

        _vary_on = d.pop("vary_on", UNSET)
        vary_on: list[EdgeRuleCacheActionVaryOnItem] | Unset = UNSET
        if _vary_on is not UNSET:
            vary_on = []
            for vary_on_item_data in _vary_on:
                vary_on_item = check_edge_rule_cache_action_vary_on_item(vary_on_item_data)

                vary_on.append(vary_on_item)

        _methods = d.pop("methods", UNSET)
        methods: list[EdgeRuleCacheActionMethodsItem] | Unset = UNSET
        if _methods is not UNSET:
            methods = []
            for methods_item_data in _methods:
                methods_item = check_edge_rule_cache_action_methods_item(methods_item_data)

                methods.append(methods_item)

        edge_rule_cache_action = cls(
            max_age_seconds=max_age_seconds,
            stale_if_error_seconds=stale_if_error_seconds,
            stale_while_revalidate_seconds=stale_while_revalidate_seconds,
            vary_on=vary_on,
            methods=methods,
        )

        edge_rule_cache_action.additional_properties = d
        return edge_rule_cache_action

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
