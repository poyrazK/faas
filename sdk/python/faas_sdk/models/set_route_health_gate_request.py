from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.set_route_health_gate_request_mode import (
    SetRouteHealthGateRequestMode,
    check_set_route_health_gate_request_mode,
)
from ..models.set_route_health_gate_request_on_regression import (
    SetRouteHealthGateRequestOnRegression,
    check_set_route_health_gate_request_on_regression,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_route import RouteHealthRoute


T = TypeVar("T", bound="SetRouteHealthGateRequest")


@_attrs_define
class SetRouteHealthGateRequest:
    mode: SetRouteHealthGateRequestMode
    expected_revision: int
    routes: list[RouteHealthRoute]
    on_regression: SetRouteHealthGateRequestOnRegression | Unset = UNSET
    """Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for
    confirmed route 5xx regressions during an enforced canary; report mode is observational."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        expected_revision = self.expected_revision

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        on_regression: str | Unset = UNSET
        if not isinstance(self.on_regression, Unset):
            on_regression = self.on_regression

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "expected_revision": expected_revision,
                "routes": routes,
            }
        )
        if on_regression is not UNSET:
            field_dict["on_regression"] = on_regression

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_route import RouteHealthRoute

        d = dict(src_dict)
        mode = check_set_route_health_gate_request_mode(d.pop("mode"))

        expected_revision = d.pop("expected_revision")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteHealthRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _on_regression = d.pop("on_regression", UNSET)
        on_regression: SetRouteHealthGateRequestOnRegression | Unset
        if isinstance(_on_regression, Unset):
            on_regression = UNSET
        else:
            on_regression = check_set_route_health_gate_request_on_regression(_on_regression)

        set_route_health_gate_request = cls(
            mode=mode,
            expected_revision=expected_revision,
            routes=routes,
            on_regression=on_regression,
        )

        set_route_health_gate_request.additional_properties = d
        return set_route_health_gate_request

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
