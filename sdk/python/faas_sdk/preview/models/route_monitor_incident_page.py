from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_monitor_incident import RouteMonitorIncident


T = TypeVar("T", bound="RouteMonitorIncidentPage")


@_attrs_define
class RouteMonitorIncidentPage:
    """Bounded owned incident history ordered by opening time and UUID descending."""

    app_id: UUID
    incidents: list[RouteMonitorIncident]
    next_before: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        incidents = []
        for incidents_item_data in self.incidents:
            incidents_item = incidents_item_data.to_dict()
            incidents.append(incidents_item)

        next_before: str | Unset = UNSET
        if not isinstance(self.next_before, Unset):
            next_before = str(self.next_before)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "incidents": incidents,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_monitor_incident import RouteMonitorIncident

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        incidents = []
        _incidents = d.pop("incidents")
        for incidents_item_data in _incidents:
            incidents_item = RouteMonitorIncident.from_dict(incidents_item_data)

            incidents.append(incidents_item)

        _next_before = d.pop("next_before", UNSET)
        next_before: UUID | Unset
        if isinstance(_next_before, Unset):
            next_before = UNSET
        else:
            next_before = UUID(_next_before)

        route_monitor_incident_page = cls(
            app_id=app_id,
            incidents=incidents,
            next_before=next_before,
        )

        route_monitor_incident_page.additional_properties = d
        return route_monitor_incident_page

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
