from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_timeline_event_kind import ReleaseTimelineEventKind, check_release_timeline_event_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.rollout_decision import RolloutDecision


T = TypeVar("T", bound="ReleaseTimelineEvent")


@_attrs_define
class ReleaseTimelineEvent:
    """One thing that happened to a release."""

    at: datetime.datetime
    kind: ReleaseTimelineEventKind
    summary: str
    """What happened, in one sentence."""
    actor: str | Unset = UNSET
    """A person's email, or Gregale for automatic steps."""
    traffic_percent: int | Unset = UNSET
    """The release's traffic share after this event."""
    repeats: int | Unset = UNSET
    """Further holds with the same rule this event stands for."""
    decision: RolloutDecision | Unset = UNSET
    """Evidence behind one decision about a rollout (ADR-1447): why a canary stage advanced, is held, or was
    aborted. Aggregates only; no request payloads, routes, URLs or customer text."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        at = self.at.isoformat()

        kind: str = self.kind

        summary = self.summary

        actor = self.actor

        traffic_percent = self.traffic_percent

        repeats = self.repeats

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "at": at,
                "kind": kind,
                "summary": summary,
            }
        )
        if actor is not UNSET:
            field_dict["actor"] = actor
        if traffic_percent is not UNSET:
            field_dict["traffic_percent"] = traffic_percent
        if repeats is not UNSET:
            field_dict["repeats"] = repeats
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.rollout_decision import RolloutDecision

        d = dict(src_dict)
        at = datetime.datetime.fromisoformat(d.pop("at"))

        kind = check_release_timeline_event_kind(d.pop("kind"))

        summary = d.pop("summary")

        actor = d.pop("actor", UNSET)

        traffic_percent = d.pop("traffic_percent", UNSET)

        repeats = d.pop("repeats", UNSET)

        _decision = d.pop("decision", UNSET)
        decision: RolloutDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = RolloutDecision.from_dict(_decision)

        release_timeline_event = cls(
            at=at,
            kind=kind,
            summary=summary,
            actor=actor,
            traffic_percent=traffic_percent,
            repeats=repeats,
            decision=decision,
        )

        release_timeline_event.additional_properties = d
        return release_timeline_event

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
