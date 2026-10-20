from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_gate_mode import RouteHealthGateMode, check_route_health_gate_mode
from ..models.route_health_gate_on_regression import RouteHealthGateOnRegression, check_route_health_gate_on_regression
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_route import RouteHealthRoute


T = TypeVar("T", bound="RouteHealthGate")


@_attrs_define
class RouteHealthGate:
    """Current critical-route selection, health guard mode, recovery policy, and configuration revision."""

    app_id: UUID
    mode: RouteHealthGateMode
    revision: int
    routes: list[RouteHealthRoute]
    on_regression: RouteHealthGateOnRegression | Unset = UNSET
    """Defaults to hold, including when omitted in a replacement update. Abort opts into automatic recovery for
    confirmed route 5xx regressions during an enforced canary; report mode is observational."""
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        mode: str = self.mode

        revision = self.revision

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        on_regression: str | Unset = UNSET
        if not isinstance(self.on_regression, Unset):
            on_regression = self.on_regression

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "mode": mode,
                "revision": revision,
                "routes": routes,
            }
        )
        if on_regression is not UNSET:
            field_dict["on_regression"] = on_regression
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_route import RouteHealthRoute

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        mode = check_route_health_gate_mode(d.pop("mode"))

        revision = d.pop("revision")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = RouteHealthRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        _on_regression = d.pop("on_regression", UNSET)
        on_regression: RouteHealthGateOnRegression | Unset
        if isinstance(_on_regression, Unset):
            on_regression = UNSET
        else:
            on_regression = check_route_health_gate_on_regression(_on_regression)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        route_health_gate = cls(
            app_id=app_id,
            mode=mode,
            revision=revision,
            routes=routes,
            on_regression=on_regression,
            updated_at=updated_at,
        )

        route_health_gate.additional_properties = d
        return route_health_gate

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
