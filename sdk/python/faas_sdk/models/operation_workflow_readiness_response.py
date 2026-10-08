from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness


T = TypeVar("T", bound="OperationWorkflowReadinessResponse")


@_attrs_define
class OperationWorkflowReadinessResponse:
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    instance_id: str
    evaluated_at: datetime.datetime
    readiness: OperationWorkflowTransitionReadiness
    """Ready means declared requirements match retained reports and the proposed milestone plan. It does not
    authorize or commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only
    blockers targeting this Operation apply. Staleness and overdue deadlines are advisories rather than undeclared
    guards."""

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        evaluated_at = self.evaluated_at.isoformat()

        readiness = self.readiness.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
                "evaluated_at": evaluated_at,
                "readiness": readiness,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        readiness = OperationWorkflowTransitionReadiness.from_dict(d.pop("readiness"))

        operation_workflow_readiness_response = cls(
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            evaluated_at=evaluated_at,
            readiness=readiness,
        )

        return operation_workflow_readiness_response
