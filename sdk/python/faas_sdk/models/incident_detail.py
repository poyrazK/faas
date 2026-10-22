from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_change_event import AppChangeEvent
    from ..models.incident import Incident
    from ..models.incident_detail_log_context import IncidentDetailLogContext
    from ..models.incident_fire import IncidentFire
    from ..models.incident_update import IncidentUpdate


T = TypeVar("T", bound="IncidentDetail")


@_attrs_define
class IncidentDetail:
    incident: Incident
    """An alert rule's firing period, a route monitor incident, or a platform incident."""
    unavailable_sources: list[str]
    fires: list[IncidentFire] | Unset = UNSET
    log_context: IncidentDetailLogContext | Unset = UNSET
    """What the logs showed at the newest fire, when log search was on."""
    updates: list[IncidentUpdate] | Unset = UNSET
    changes: list[AppChangeEvent] | Unset = UNSET
    changes_truncated: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        incident = self.incident.to_dict()

        unavailable_sources = self.unavailable_sources

        fires: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.fires, Unset):
            fires = []
            for fires_item_data in self.fires:
                fires_item = fires_item_data.to_dict()
                fires.append(fires_item)

        log_context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.log_context, Unset):
            log_context = self.log_context.to_dict()

        updates: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.updates, Unset):
            updates = []
            for updates_item_data in self.updates:
                updates_item = updates_item_data.to_dict()
                updates.append(updates_item)

        changes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.changes, Unset):
            changes = []
            for changes_item_data in self.changes:
                changes_item = changes_item_data.to_dict()
                changes.append(changes_item)

        changes_truncated = self.changes_truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "incident": incident,
                "unavailable_sources": unavailable_sources,
            }
        )
        if fires is not UNSET:
            field_dict["fires"] = fires
        if log_context is not UNSET:
            field_dict["log_context"] = log_context
        if updates is not UNSET:
            field_dict["updates"] = updates
        if changes is not UNSET:
            field_dict["changes"] = changes
        if changes_truncated is not UNSET:
            field_dict["changes_truncated"] = changes_truncated

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_change_event import AppChangeEvent
        from ..models.incident import Incident
        from ..models.incident_detail_log_context import IncidentDetailLogContext
        from ..models.incident_fire import IncidentFire
        from ..models.incident_update import IncidentUpdate

        d = dict(src_dict)
        incident = Incident.from_dict(d.pop("incident"))

        unavailable_sources = cast(list[str], d.pop("unavailable_sources"))

        _fires = d.pop("fires", UNSET)
        fires: list[IncidentFire] | Unset = UNSET
        if _fires is not UNSET:
            fires = []
            for fires_item_data in _fires:
                fires_item = IncidentFire.from_dict(fires_item_data)

                fires.append(fires_item)

        _log_context = d.pop("log_context", UNSET)
        log_context: IncidentDetailLogContext | Unset
        if isinstance(_log_context, Unset):
            log_context = UNSET
        else:
            log_context = IncidentDetailLogContext.from_dict(_log_context)

        _updates = d.pop("updates", UNSET)
        updates: list[IncidentUpdate] | Unset = UNSET
        if _updates is not UNSET:
            updates = []
            for updates_item_data in _updates:
                updates_item = IncidentUpdate.from_dict(updates_item_data)

                updates.append(updates_item)

        _changes = d.pop("changes", UNSET)
        changes: list[AppChangeEvent] | Unset = UNSET
        if _changes is not UNSET:
            changes = []
            for changes_item_data in _changes:
                changes_item = AppChangeEvent.from_dict(changes_item_data)

                changes.append(changes_item)

        changes_truncated = d.pop("changes_truncated", UNSET)

        incident_detail = cls(
            incident=incident,
            unavailable_sources=unavailable_sources,
            fires=fires,
            log_context=log_context,
            updates=updates,
            changes=changes,
            changes_truncated=changes_truncated,
        )

        incident_detail.additional_properties = d
        return incident_detail

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
