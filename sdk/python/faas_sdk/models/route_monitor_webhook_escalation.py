from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RouteMonitorWebhookEscalation")


@_attrs_define
class RouteMonitorWebhookEscalation:
    """Counts newly violated route and signal budgets since the prior incident evaluation."""

    previous_checked_at: datetime.datetime
    newly_violated_routes: int
    newly_violated_signals: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        previous_checked_at = self.previous_checked_at.isoformat()

        newly_violated_routes = self.newly_violated_routes

        newly_violated_signals = self.newly_violated_signals

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "previous_checked_at": previous_checked_at,
                "newly_violated_routes": newly_violated_routes,
                "newly_violated_signals": newly_violated_signals,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        previous_checked_at = datetime.datetime.fromisoformat(d.pop("previous_checked_at"))

        newly_violated_routes = d.pop("newly_violated_routes")

        newly_violated_signals = d.pop("newly_violated_signals")

        route_monitor_webhook_escalation = cls(
            previous_checked_at=previous_checked_at,
            newly_violated_routes=newly_violated_routes,
            newly_violated_signals=newly_violated_signals,
        )

        route_monitor_webhook_escalation.additional_properties = d
        return route_monitor_webhook_escalation

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
