from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.wake_timeline_event_data import WakeTimelineEventData


T = TypeVar("T", bound="WakeTimelineEvent")


@_attrs_define
class WakeTimelineEvent:
    """One frame of the wake timeline (issue #517 / PR-C /
    ADR-064). The shape mirrors the typed event payloads
    the producers write — see `pkg/events/wake.go`. The
    canonical `wake.*` vocabulary is documented in
    `docs/adr/064-wake-timeline-canonical-vocabulary.md`
    (including `wake.restore_breakdown`, which exposes the
    vmmd snapshot-restore phases in integer milliseconds, plus
    the aggregate `total_ms`; and build/deploy/boot failure kinds).

    """

    at: datetime.datetime
    """RFC 3339 UTC. Oldest-first (forward narrative)."""
    kind: str
    """Canonical `wake.*` kind. See ADR-064."""
    actor: str
    """Daemon that wrote the row (`schedd` / `vmmd` / `gatewayd` / `egress` / `builderd` / `apid`)."""
    data: WakeTimelineEventData
    """Producer-supplied payload (json object)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind = self.kind

        actor = self.actor

        data = self.data.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
                "actor": actor,
                "data": data,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.wake_timeline_event_data import WakeTimelineEventData

        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = d.pop("kind")

        actor = d.pop("actor")

        data = WakeTimelineEventData.from_dict(d.pop("data"))

        wake_timeline_event = cls(
            at=at,
            kind=kind,
            actor=actor,
            data=data,
        )

        wake_timeline_event.additional_properties = d
        return wake_timeline_event

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
