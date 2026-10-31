from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.obs_wake_event_data import ObsWakeEventData


T = TypeVar("T", bound="ObsWakeEvent")


@_attrs_define
class ObsWakeEvent:
    """One events row of a wake."""

    at: datetime.datetime
    kind: str
    actor: str | Unset = UNSET
    subject: str | Unset = UNSET
    trace_id: str | Unset = UNSET
    data: ObsWakeEventData | Unset = UNSET
    """The event's payload as recorded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind = self.kind

        actor = self.actor

        subject = self.subject

        trace_id = self.trace_id

        data: dict[str, Any] | Unset = UNSET
        if not isinstance(self.data, Unset):
            data = self.data.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
            }
        )
        if actor is not UNSET:
            field_dict["actor"] = actor
        if subject is not UNSET:
            field_dict["subject"] = subject
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if data is not UNSET:
            field_dict["data"] = data

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_wake_event_data import ObsWakeEventData

        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = d.pop("kind")

        actor = d.pop("actor", UNSET)

        subject = d.pop("subject", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        _data = d.pop("data", UNSET)
        data: ObsWakeEventData | Unset
        if isinstance(_data, Unset):
            data = UNSET
        else:
            data = ObsWakeEventData.from_dict(_data)

        obs_wake_event = cls(
            at=at,
            kind=kind,
            actor=actor,
            subject=subject,
            trace_id=trace_id,
            data=data,
        )

        obs_wake_event.additional_properties = d
        return obs_wake_event

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
