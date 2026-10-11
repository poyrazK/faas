from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowAttentionStats")


@_attrs_define
class OperationWorkflowAttentionStats:
    """Aggregate counts and age measurements for workflows, blockers, deadlines, and unresolved dependencies."""

    sla_at_risk_workflow_count: int
    sla_breached_workflow_count: int
    sla_unknown_workflow_count: int
    escalated_workflow_count: int
    awaiting_verification_workflow_count: int
    awaiting_verification_resolution_count: int
    low_blocker_count: int
    normal_blocker_count: int
    high_blocker_count: int
    urgent_blocker_count: int
    unacknowledged_blocker_count: int
    follow_up_overdue_blocker_count: int
    escalated_blocker_count: int
    dependency_workflow_count: int
    dependency_count: int
    overdue_workflow_count: int
    workflow_count: int
    blocked_workflow_count: int
    stale_workflow_count: int
    blocker_count: int
    unknown_age_blockers: int
    earliest_overdue_deadline_at: datetime.datetime | Unset = UNSET
    longest_overdue_seconds: int | Unset = UNSET
    oldest_blocker_at: datetime.datetime | Unset = UNSET
    oldest_blocker_age_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        sla_at_risk_workflow_count = self.sla_at_risk_workflow_count

        sla_breached_workflow_count = self.sla_breached_workflow_count

        sla_unknown_workflow_count = self.sla_unknown_workflow_count

        escalated_workflow_count = self.escalated_workflow_count

        awaiting_verification_workflow_count = self.awaiting_verification_workflow_count

        awaiting_verification_resolution_count = self.awaiting_verification_resolution_count

        low_blocker_count = self.low_blocker_count

        normal_blocker_count = self.normal_blocker_count

        high_blocker_count = self.high_blocker_count

        urgent_blocker_count = self.urgent_blocker_count

        unacknowledged_blocker_count = self.unacknowledged_blocker_count

        follow_up_overdue_blocker_count = self.follow_up_overdue_blocker_count

        escalated_blocker_count = self.escalated_blocker_count

        dependency_workflow_count = self.dependency_workflow_count

        dependency_count = self.dependency_count

        overdue_workflow_count = self.overdue_workflow_count

        workflow_count = self.workflow_count

        blocked_workflow_count = self.blocked_workflow_count

        stale_workflow_count = self.stale_workflow_count

        blocker_count = self.blocker_count

        unknown_age_blockers = self.unknown_age_blockers

        earliest_overdue_deadline_at: str | Unset = UNSET
        if not isinstance(self.earliest_overdue_deadline_at, Unset):
            earliest_overdue_deadline_at = self.earliest_overdue_deadline_at.isoformat()

        longest_overdue_seconds = self.longest_overdue_seconds

        oldest_blocker_at: str | Unset = UNSET
        if not isinstance(self.oldest_blocker_at, Unset):
            oldest_blocker_at = self.oldest_blocker_at.isoformat()

        oldest_blocker_age_seconds = self.oldest_blocker_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "sla_at_risk_workflow_count": sla_at_risk_workflow_count,
                "sla_breached_workflow_count": sla_breached_workflow_count,
                "sla_unknown_workflow_count": sla_unknown_workflow_count,
                "escalated_workflow_count": escalated_workflow_count,
                "awaiting_verification_workflow_count": awaiting_verification_workflow_count,
                "awaiting_verification_resolution_count": awaiting_verification_resolution_count,
                "low_blocker_count": low_blocker_count,
                "normal_blocker_count": normal_blocker_count,
                "high_blocker_count": high_blocker_count,
                "urgent_blocker_count": urgent_blocker_count,
                "unacknowledged_blocker_count": unacknowledged_blocker_count,
                "follow_up_overdue_blocker_count": follow_up_overdue_blocker_count,
                "escalated_blocker_count": escalated_blocker_count,
                "dependency_workflow_count": dependency_workflow_count,
                "dependency_count": dependency_count,
                "overdue_workflow_count": overdue_workflow_count,
                "workflow_count": workflow_count,
                "blocked_workflow_count": blocked_workflow_count,
                "stale_workflow_count": stale_workflow_count,
                "blocker_count": blocker_count,
                "unknown_age_blockers": unknown_age_blockers,
            }
        )
        if earliest_overdue_deadline_at is not UNSET:
            field_dict["earliest_overdue_deadline_at"] = earliest_overdue_deadline_at
        if longest_overdue_seconds is not UNSET:
            field_dict["longest_overdue_seconds"] = longest_overdue_seconds
        if oldest_blocker_at is not UNSET:
            field_dict["oldest_blocker_at"] = oldest_blocker_at
        if oldest_blocker_age_seconds is not UNSET:
            field_dict["oldest_blocker_age_seconds"] = oldest_blocker_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sla_at_risk_workflow_count = d.pop("sla_at_risk_workflow_count")

        sla_breached_workflow_count = d.pop("sla_breached_workflow_count")

        sla_unknown_workflow_count = d.pop("sla_unknown_workflow_count")

        escalated_workflow_count = d.pop("escalated_workflow_count")

        awaiting_verification_workflow_count = d.pop("awaiting_verification_workflow_count")

        awaiting_verification_resolution_count = d.pop("awaiting_verification_resolution_count")

        low_blocker_count = d.pop("low_blocker_count")

        normal_blocker_count = d.pop("normal_blocker_count")

        high_blocker_count = d.pop("high_blocker_count")

        urgent_blocker_count = d.pop("urgent_blocker_count")

        unacknowledged_blocker_count = d.pop("unacknowledged_blocker_count")

        follow_up_overdue_blocker_count = d.pop("follow_up_overdue_blocker_count")

        escalated_blocker_count = d.pop("escalated_blocker_count")

        dependency_workflow_count = d.pop("dependency_workflow_count")

        dependency_count = d.pop("dependency_count")

        overdue_workflow_count = d.pop("overdue_workflow_count")

        workflow_count = d.pop("workflow_count")

        blocked_workflow_count = d.pop("blocked_workflow_count")

        stale_workflow_count = d.pop("stale_workflow_count")

        blocker_count = d.pop("blocker_count")

        unknown_age_blockers = d.pop("unknown_age_blockers")

        _earliest_overdue_deadline_at = d.pop("earliest_overdue_deadline_at", UNSET)
        earliest_overdue_deadline_at: datetime.datetime | Unset
        if isinstance(_earliest_overdue_deadline_at, Unset):
            earliest_overdue_deadline_at = UNSET
        else:
            earliest_overdue_deadline_at = datetime.datetime.fromisoformat(_earliest_overdue_deadline_at)

        longest_overdue_seconds = d.pop("longest_overdue_seconds", UNSET)

        _oldest_blocker_at = d.pop("oldest_blocker_at", UNSET)
        oldest_blocker_at: datetime.datetime | Unset
        if isinstance(_oldest_blocker_at, Unset):
            oldest_blocker_at = UNSET
        else:
            oldest_blocker_at = datetime.datetime.fromisoformat(_oldest_blocker_at)

        oldest_blocker_age_seconds = d.pop("oldest_blocker_age_seconds", UNSET)

        operation_workflow_attention_stats = cls(
            sla_at_risk_workflow_count=sla_at_risk_workflow_count,
            sla_breached_workflow_count=sla_breached_workflow_count,
            sla_unknown_workflow_count=sla_unknown_workflow_count,
            escalated_workflow_count=escalated_workflow_count,
            awaiting_verification_workflow_count=awaiting_verification_workflow_count,
            awaiting_verification_resolution_count=awaiting_verification_resolution_count,
            low_blocker_count=low_blocker_count,
            normal_blocker_count=normal_blocker_count,
            high_blocker_count=high_blocker_count,
            urgent_blocker_count=urgent_blocker_count,
            unacknowledged_blocker_count=unacknowledged_blocker_count,
            follow_up_overdue_blocker_count=follow_up_overdue_blocker_count,
            escalated_blocker_count=escalated_blocker_count,
            dependency_workflow_count=dependency_workflow_count,
            dependency_count=dependency_count,
            overdue_workflow_count=overdue_workflow_count,
            workflow_count=workflow_count,
            blocked_workflow_count=blocked_workflow_count,
            stale_workflow_count=stale_workflow_count,
            blocker_count=blocker_count,
            unknown_age_blockers=unknown_age_blockers,
            earliest_overdue_deadline_at=earliest_overdue_deadline_at,
            longest_overdue_seconds=longest_overdue_seconds,
            oldest_blocker_at=oldest_blocker_at,
            oldest_blocker_age_seconds=oldest_blocker_age_seconds,
        )

        return operation_workflow_attention_stats
