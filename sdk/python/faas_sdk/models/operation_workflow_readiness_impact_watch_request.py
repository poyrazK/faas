from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_impact_watch_request_kind import (
    OperationWorkflowReadinessImpactWatchRequestKind,
    check_operation_workflow_readiness_impact_watch_request_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessImpactWatchRequest")


@_attrs_define
class OperationWorkflowReadinessImpactWatchRequest:
    """OperationWorkflowReadinessImpactWatchRequest. Protected business result monitoring of retained unresolved human
    reviews in a scoped application environment; advisory confidence only.

    """

    workflow: str
    """OperationWorkflowReadinessImpactWatchRequest.workflow. Exact workflow identity."""
    kind: OperationWorkflowReadinessImpactWatchRequestKind
    """OperationWorkflowReadinessImpactWatchRequest.kind. Protected result category."""
    code: str
    """OperationWorkflowReadinessImpactWatchRequest.code. Exact protected result slug."""
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowReadinessImpactWatchRequest.acknowledge_after_seconds. Optional response window from
    delivery of critical or overdue impact alerts, requires a nonempty owner. Zero omits the target; clearance
    notices have no target. Acknowledgment is independent of resolving source review obligations."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowReadinessImpactWatchRequest.fallback_owner. Optional distinct fallback team, requires a
    positive response window. Guarded handoff preserves manual assignment history and the original deadline."""
    owner: str | Unset = UNSET
    """OperationWorkflowReadinessImpactWatchRequest.owner. Optional team assigned new impact notifications; does
    not filter review queue source owners."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        kind: str = self.kind

        code = self.code

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        owner = self.owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "kind": kind,
                "code": code,
            }
        )
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if owner is not UNSET:
            field_dict["owner"] = owner

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workflow = d.pop("workflow")

        kind = check_operation_workflow_readiness_impact_watch_request_kind(d.pop("kind"))

        code = d.pop("code")

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        owner = d.pop("owner", UNSET)

        operation_workflow_readiness_impact_watch_request = cls(
            workflow=workflow,
            kind=kind,
            code=code,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
            owner=owner,
        )

        return operation_workflow_readiness_impact_watch_request
