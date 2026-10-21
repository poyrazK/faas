from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_insight_fix_action import RouteInsightFixAction, check_route_insight_fix_action
from ..models.route_insight_kind import RouteInsightKind, check_route_insight_kind
from ..models.route_insight_state import RouteInsightState, check_route_insight_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_insight_evidence import RouteInsightEvidence
    from ..models.route_insight_fix import RouteInsightFix


T = TypeVar("T", bound="RouteInsight")


@_attrs_define
class RouteInsight:
    """One finding for a route; also the payload of the routes.insight.detected and routes.insight.resolved webhooks."""

    id: UUID
    kind: RouteInsightKind
    method: str
    route: str
    """Route template, for example /catalog."""
    state: RouteInsightState
    message: str
    """What was observed and what to change."""
    evidence: RouteInsightEvidence
    """Measured figures behind the insight: awake share, interval and savings for kept_awake; safe max-age and
    repeat counts for cacheable; the rule's max-age, refills, stale refills and measured safe max-age for
    cache_stale and cache_too_short."""
    first_detected_at: datetime.datetime
    last_detected_at: datetime.datetime
    resolved_at: datetime.datetime | Unset = UNSET
    fixed_at: datetime.datetime | Unset = UNSET
    """When an applied fix resolved the insight."""
    fix_action: RouteInsightFixAction | Unset = UNSET
    """The fix that resolved the insight."""
    fix: RouteInsightFix | Unset = UNSET
    """The edge-rule change applying an active insight would make now, planned from its evidence and the app's
    current rules (ADR-20261020161310). Absent when the platform has no fix; never in webhook payloads."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

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

        fixed_at: str | Unset = UNSET
        if not isinstance(self.fixed_at, Unset):
            fixed_at = self.fixed_at.isoformat()

        fix_action: str | Unset = UNSET
        if not isinstance(self.fix_action, Unset):
            fix_action = self.fix_action

        fix: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fix, Unset):
            fix = self.fix.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
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
        if fixed_at is not UNSET:
            field_dict["fixed_at"] = fixed_at
        if fix_action is not UNSET:
            field_dict["fix_action"] = fix_action
        if fix is not UNSET:
            field_dict["fix"] = fix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_insight_evidence import RouteInsightEvidence
        from ..models.route_insight_fix import RouteInsightFix

        d = dict(src_dict)
        id = UUID(d.pop("id"))

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

        _fixed_at = d.pop("fixed_at", UNSET)
        fixed_at: datetime.datetime | Unset
        if isinstance(_fixed_at, Unset):
            fixed_at = UNSET
        else:
            fixed_at = datetime.datetime.fromisoformat(_fixed_at)

        _fix_action = d.pop("fix_action", UNSET)
        fix_action: RouteInsightFixAction | Unset
        if isinstance(_fix_action, Unset):
            fix_action = UNSET
        else:
            fix_action = check_route_insight_fix_action(_fix_action)

        _fix = d.pop("fix", UNSET)
        fix: RouteInsightFix | Unset
        if isinstance(_fix, Unset):
            fix = UNSET
        else:
            fix = RouteInsightFix.from_dict(_fix)

        route_insight = cls(
            id=id,
            kind=kind,
            method=method,
            route=route,
            state=state,
            message=message,
            evidence=evidence,
            first_detected_at=first_detected_at,
            last_detected_at=last_detected_at,
            resolved_at=resolved_at,
            fixed_at=fixed_at,
            fix_action=fix_action,
            fix=fix,
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
