from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_impact_watch_kind import (
    OperationWorkflowReadinessImpactWatchKind,
    check_operation_workflow_readiness_impact_watch_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary


T = TypeVar("T", bound="OperationWorkflowReadinessImpactWatch")


@_attrs_define
class OperationWorkflowReadinessImpactWatch:
    """OperationWorkflowReadinessImpactWatch. Protected business result monitoring of retained unresolved human reviews in
    a scoped application environment; advisory confidence only.

    """

    workflow: str
    """OperationWorkflowReadinessImpactWatch.workflow. Exact workflow identity."""
    kind: OperationWorkflowReadinessImpactWatchKind
    """OperationWorkflowReadinessImpactWatch.kind. Protected result category."""
    code: str
    """OperationWorkflowReadinessImpactWatch.code. Exact protected result slug."""
    evaluated_at: datetime.datetime
    """OperationWorkflowReadinessImpactWatch.evaluated_at. Time of the last accepted baseline observation."""
    coverage_incomplete: bool
    """OperationWorkflowReadinessImpactWatch.coverage_incomplete. Candidate-window or 64-obligation tracking limit,
    missing source, or unresolved source after queue disappearance suppresses a clearance assertion."""
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowReadinessImpactWatch.acknowledge_after_seconds. Optional response window from delivery of
    critical or overdue impact alerts, requires a nonempty owner. Zero omits the target; clearance notices have no
    target. Acknowledgment is independent of resolving source review obligations."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReadinessImpactWatch.fallback_owner. Optional distinct fallback team, requires a positive
    response window. Guarded handoff preserves manual assignment history and the original deadline."""
    owner: str | Unset = UNSET
    """OperationWorkflowReadinessImpactWatch.owner. Optional team assigned new impact notifications; does not
    filter review queue source owners."""
    current: OperationWorkflowReadinessImpactSummary | Unset = UNSET
    """OperationWorkflowReadinessImpactSummary. Retained unresolved review confidence by workflow and protected
    business result, separate from reported business outcome."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        kind: str = self.kind

        code = self.code

        evaluated_at = self.evaluated_at.isoformat()

        coverage_incomplete = self.coverage_incomplete

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        owner = self.owner

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "kind": kind,
                "code": code,
                "evaluated_at": evaluated_at,
                "coverage_incomplete": coverage_incomplete,
            }
        )
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if owner is not UNSET:
            field_dict["owner"] = owner
        if current is not UNSET:
            field_dict["current"] = current

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_impact_summary import OperationWorkflowReadinessImpactSummary

        d = dict(src_dict)
        workflow = d.pop("workflow")

        kind = check_operation_workflow_readiness_impact_watch_kind(d.pop("kind"))

        code = d.pop("code")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        coverage_incomplete = d.pop("coverage_incomplete")

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        owner = d.pop("owner", UNSET)

        _current = d.pop("current", UNSET)
        current: OperationWorkflowReadinessImpactSummary | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = OperationWorkflowReadinessImpactSummary.from_dict(_current)

        operation_workflow_readiness_impact_watch = cls(
            workflow=workflow,
            kind=kind,
            code=code,
            evaluated_at=evaluated_at,
            coverage_incomplete=coverage_incomplete,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
            owner=owner,
            current=current,
        )

        return operation_workflow_readiness_impact_watch
