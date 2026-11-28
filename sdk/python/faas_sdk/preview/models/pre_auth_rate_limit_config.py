from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_rate_limit_config_mode import PreAuthRateLimitConfigMode, check_pre_auth_rate_limit_config_mode
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.pre_auth_route_limit import PreAuthRouteLimit


T = TypeVar("T", bound="PreAuthRateLimitConfig")


@_attrs_define
class PreAuthRateLimitConfig:
    """Optional per-source gateway limit evaluated before consumer-key lookup, JWT verification, and VM wake. App-wide and
    failed-response budgets are replica-local; exact routes can opt into shared request budgets. Observe mode records
    threshold crossings without rejecting requests.

    """

    mode: PreAuthRateLimitConfigMode
    requests_per_second: int | Unset = UNSET
    """Required in observe/enforce mode and bounded by the app plan's request rate."""
    burst: int | Unset = UNSET
    """Required in observe/enforce mode and bounded by the app plan's request burst."""
    routes: list[PreAuthRouteLimit] | Unset = UNSET
    """Optional exact public method/path limits, evaluated in addition to the app-wide source limit. Each route
    rate and burst must be no greater than the app-wide values."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        requests_per_second = self.requests_per_second

        burst = self.burst

        routes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = []
            for routes_item_data in self.routes:
                routes_item = routes_item_data.to_dict()
                routes.append(routes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
            }
        )
        if requests_per_second is not UNSET:
            field_dict["requests_per_second"] = requests_per_second
        if burst is not UNSET:
            field_dict["burst"] = burst
        if routes is not UNSET:
            field_dict["routes"] = routes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.pre_auth_route_limit import PreAuthRouteLimit

        d = dict(src_dict)
        mode = check_pre_auth_rate_limit_config_mode(d.pop("mode"))

        requests_per_second = d.pop("requests_per_second", UNSET)

        burst = d.pop("burst", UNSET)

        _routes = d.pop("routes", UNSET)
        routes: list[PreAuthRouteLimit] | Unset = UNSET
        if _routes is not UNSET:
            routes = []
            for routes_item_data in _routes:
                routes_item = PreAuthRouteLimit.from_dict(routes_item_data)

                routes.append(routes_item)

        pre_auth_rate_limit_config = cls(
            mode=mode,
            requests_per_second=requests_per_second,
            burst=burst,
            routes=routes,
        )

        pre_auth_rate_limit_config.additional_properties = d
        return pre_auth_rate_limit_config

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
