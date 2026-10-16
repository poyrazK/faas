from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_insight_kind import RouteInsightKind, check_route_insight_kind
from ..models.route_insight_state import RouteInsightState, check_route_insight_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_insight_evidence import RouteInsightEvidence


T = TypeVar("T", bound="RouteInsight")


@_attrs_define
class RouteInsight:
    """One finding for a route; also the payload of the routes.insight.detected and routes.insight.resolved webhooks."""

    kind: RouteInsightKind
    method: str
    route: str
    """Route template, for example /catalog."""
    state: RouteInsightState
    message: str
    """What was observed and what to change."""
    evidence: RouteInsightEvidence
    """Measured figures behind the insight: awake share, interval and savings for kept_awake; safe max-age and
    repeat counts for cacheable."""
    first_detected_at: datetime.datetime
    last_detected_at: datetime.datetime
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        method = self.method

        route = self.route

        state: str = self.state

        message = self.message

        evidence = self.evidence.to_dict()

        first_detected_at = self.first_detected_at.isoformat()

        last_detected_at = self.last_detected_at.isoformat()

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "method": method,
                "route": route,
                "state": state,
                "message": message,
                "evidence": evidence,
                "first_detected_at": first_detected_at,
                "last_detected_at": last_detected_at,
            }
        )
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_insight_evidence import RouteInsightEvidence

        d = dict(src_dict)
        kind = check_route_insight_kind(d.pop("kind"))

        method = d.pop("method")

        route = d.pop("route")

        state = check_route_insight_state(d.pop("state"))

        message = d.pop("message")

        evidence = RouteInsightEvidence.from_dict(d.pop("evidence"))

        first_detected_at = datetime.datetime.fromisoformat(d.pop("first_detected_at"))

        last_detected_at = datetime.datetime.fromisoformat(d.pop("last_detected_at"))

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        route_insight = cls(
            kind=kind,
            method=method,
            route=route,
            state=state,
            message=message,
            evidence=evidence,
            first_detected_at=first_detected_at,
            last_detected_at=last_detected_at,
            resolved_at=resolved_at,
        )

        route_insight.additional_properties = d
        return route_insight

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
