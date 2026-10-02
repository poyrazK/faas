from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_health_history_entry_purpose import (
    RouteHealthHistoryEntryPurpose,
    check_route_health_history_entry_purpose,
)
from ..models.route_health_history_entry_source import (
    RouteHealthHistoryEntrySource,
    check_route_health_history_entry_source,
)
from ..models.route_health_history_entry_version import (
    RouteHealthHistoryEntryVersion,
    check_route_health_history_entry_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_health_decision import RouteHealthDecision
    from ..models.route_health_evaluation_policy import RouteHealthEvaluationPolicy
    from ..models.route_health_report import RouteHealthReport


T = TypeVar("T", bound="RouteHealthHistoryEntry")


@_attrs_define
class RouteHealthHistoryEntry:
    """Immutable evidence from a real canary advance evaluation or committed automatic route health abort, bounded to 64
    KiB encoded JSON. Identical advance retries retain the original id and checked_at.

    """

    version: RouteHealthHistoryEntryVersion
    id: UUID
    checked_at: datetime.datetime
    source: RouteHealthHistoryEntrySource
    traffic_percent: int
    """Candidate traffic before evaluation."""
    requested_traffic_percent: int
    policy: RouteHealthEvaluationPolicy
    """Versioned thresholds captured with the decision; never inferred from current settings."""
    decision: RouteHealthDecision
    """Metadata-only decision evaluated inside the canary traffic transaction; history_id correlates the exact
    saved evidence with the advance response and traffic audit."""
    report: RouteHealthReport
    purpose: RouteHealthHistoryEntryPurpose | Unset = UNSET
    """Abort identifies a committed automatic rollback with worker source and requested traffic 0. Omitted for
    advance evaluations."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        id = str(self.id)

        checked_at = self.checked_at.isoformat()

        source: str = self.source

        traffic_percent = self.traffic_percent

        requested_traffic_percent = self.requested_traffic_percent

        policy = self.policy.to_dict()

        decision = self.decision.to_dict()

        report = self.report.to_dict()

        purpose: str | Unset = UNSET
        if not isinstance(self.purpose, Unset):
            purpose = self.purpose

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "checked_at": checked_at,
                "source": source,
                "traffic_percent": traffic_percent,
                "requested_traffic_percent": requested_traffic_percent,
                "policy": policy,
                "decision": decision,
                "report": report,
            }
        )
        if purpose is not UNSET:
            field_dict["purpose"] = purpose

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_health_decision import RouteHealthDecision
        from ..models.route_health_evaluation_policy import RouteHealthEvaluationPolicy
        from ..models.route_health_report import RouteHealthReport

        d = dict(src_dict)
        version = check_route_health_history_entry_version(d.pop("version"))

        id = UUID(d.pop("id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        source = check_route_health_history_entry_source(d.pop("source"))

        traffic_percent = d.pop("traffic_percent")

        requested_traffic_percent = d.pop("requested_traffic_percent")

        policy = RouteHealthEvaluationPolicy.from_dict(d.pop("policy"))

        decision = RouteHealthDecision.from_dict(d.pop("decision"))

        report = RouteHealthReport.from_dict(d.pop("report"))

        _purpose = d.pop("purpose", UNSET)
        purpose: RouteHealthHistoryEntryPurpose | Unset
        if isinstance(_purpose, Unset):
            purpose = UNSET
        else:
            purpose = check_route_health_history_entry_purpose(_purpose)

        route_health_history_entry = cls(
            version=version,
            id=id,
            checked_at=checked_at,
            source=source,
            traffic_percent=traffic_percent,
            requested_traffic_percent=requested_traffic_percent,
            policy=policy,
            decision=decision,
            report=report,
            purpose=purpose,
        )

        route_health_history_entry.additional_properties = d
        return route_health_history_entry

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
