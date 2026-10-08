from __future__ import annotations
import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
T = TypeVar("T", bound="OperationWorkflowAttentionStats")
@define
class OperationWorkflowAttentionStats:
    workflow_count: int
    blocked_workflow_count: int
    stale_workflow_count: int
    blocker_count: int
    unknown_age_blockers: int
    dependency_workflow_count: int = 0
    dependency_count: int = 0
    overdue_workflow_count: int = 0
    earliest_overdue_deadline_at: datetime.datetime | Unset = UNSET
    longest_overdue_seconds: int | Unset = UNSET
    oldest_blocker_at: datetime.datetime | Unset = UNSET
    oldest_blocker_age_seconds: int | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result = {key: getattr(self,key) for key in ("workflow_count","blocked_workflow_count","stale_workflow_count","blocker_count","unknown_age_blockers")}
        if self.oldest_blocker_at is not UNSET: result["oldest_blocker_at"] = self.oldest_blocker_at.isoformat()
        if self.oldest_blocker_age_seconds is not UNSET: result["oldest_blocker_age_seconds"] = self.oldest_blocker_age_seconds
        result["overdue_workflow_count"] = self.overdue_workflow_count
        if self.earliest_overdue_deadline_at is not UNSET: result["earliest_overdue_deadline_at"] = self.earliest_overdue_deadline_at.isoformat()
        if self.longest_overdue_seconds is not UNSET: result["longest_overdue_seconds"] = self.longest_overdue_seconds
        result["dependency_workflow_count"], result["dependency_count"] = self.dependency_workflow_count, self.dependency_count
        return result
    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        data = {key: src_dict[key] for key in ("workflow_count","blocked_workflow_count","stale_workflow_count","blocker_count","unknown_age_blockers")}
        if src_dict.get("oldest_blocker_at") is not None: data["oldest_blocker_at"] = datetime.datetime.fromisoformat(src_dict["oldest_blocker_at"])
        if src_dict.get("oldest_blocker_age_seconds") is not None: data["oldest_blocker_age_seconds"] = src_dict["oldest_blocker_age_seconds"]
        data["overdue_workflow_count"] = src_dict.get("overdue_workflow_count", 0)
        if src_dict.get("earliest_overdue_deadline_at") is not None: data["earliest_overdue_deadline_at"] = datetime.datetime.fromisoformat(src_dict["earliest_overdue_deadline_at"])
        if src_dict.get("longest_overdue_seconds") is not None: data["longest_overdue_seconds"] = src_dict["longest_overdue_seconds"]
        data["dependency_workflow_count"], data["dependency_count"] = src_dict.get("dependency_workflow_count",0), src_dict.get("dependency_count",0)
        return cls(**data)
