from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowAttentionStats")


@_attrs_define
class OperationWorkflowAttentionStats:
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
