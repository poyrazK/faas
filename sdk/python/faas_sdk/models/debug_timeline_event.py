from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_timeline_event_phase import DebugTimelineEventPhase, check_debug_timeline_event_phase
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugTimelineEvent")


@_attrs_define
class DebugTimelineEvent:
    """Deterministic, bounded request and wake lifecycle marker. Raw event payloads are not returned."""

    at: datetime.datetime
    phase: DebugTimelineEventPhase
    kind: str
    summary: str
    actor: str | Unset = UNSET
    duration_ms: int | Unset = UNSET
    status: int | Unset = UNSET
    approximate: bool | Unset = UNSET
    """True when the marker is derived from a collapsed minute bucket rather than an exact request timestamp."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        phase: str = self.phase

        kind = self.kind

        summary = self.summary

        actor = self.actor

        duration_ms = self.duration_ms

        status = self.status

        approximate = self.approximate

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "phase": phase,
                "kind": kind,
                "summary": summary,
            }
        )
        if actor is not UNSET:
            field_dict["actor"] = actor
        if duration_ms is not UNSET:
            field_dict["duration_ms"] = duration_ms
        if status is not UNSET:
            field_dict["status"] = status
        if approximate is not UNSET:
            field_dict["approximate"] = approximate

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        phase = check_debug_timeline_event_phase(d.pop("phase"))

        kind = d.pop("kind")

        summary = d.pop("summary")

        actor = d.pop("actor", UNSET)

        duration_ms = d.pop("duration_ms", UNSET)

        status = d.pop("status", UNSET)

        approximate = d.pop("approximate", UNSET)

        debug_timeline_event = cls(
            at=at,
            phase=phase,
            kind=kind,
            summary=summary,
            actor=actor,
            duration_ms=duration_ms,
            status=status,
            approximate=approximate,
        )

        debug_timeline_event.additional_properties = d
        return debug_timeline_event

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
