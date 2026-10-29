from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_step import OperationWorkflowStep


T = TypeVar("T", bound="OperationMilestone")


@_attrs_define
class OperationMilestone:
    """Retained public business fact. Deduplicated by Operation and milestone ID across execution generations, retained
    with the Operation, and attributed to its immutable business reference.

    """

    id: UUID
    operation_id: UUID
    name: str
    payload: Any
    """Retained, schema-validated public JSON fact. Read through the existing customer or account ownership
    boundary."""
    occurred_at: datetime.datetime
    created_at: datetime.datetime
    """First platform publication time; determines descending timeline order."""
    sequence: int
    """Corresponding Operation event sequence. The general event stream contains a notice; this ledger retains the
    payload."""
    workflow_steps: list[OperationWorkflowStep] | Unset = UNSET
    """App-declared workflow labels matched to this retained milestone; only observed steps are included."""
    platform_tenant_id: UUID | Unset = UNSET
    """Only account operator feeds include this customer identity."""
    subject: OperationSubject | Unset = UNSET
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        name = self.name

        payload = self.payload

        occurred_at = self.occurred_at.isoformat()

        created_at = self.created_at.isoformat()

        sequence = self.sequence

        workflow_steps: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflow_steps, Unset):
            workflow_steps = []
            for workflow_steps_item_data in self.workflow_steps:
                workflow_steps_item = workflow_steps_item_data.to_dict()
                workflow_steps.append(workflow_steps_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        subject: dict[str, Any] | Unset = UNSET
        if not isinstance(self.subject, Unset):
            subject = self.subject.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "operation_id": operation_id,
                "name": name,
                "payload": payload,
                "occurred_at": occurred_at,
                "created_at": created_at,
                "sequence": sequence,
            }
        )
        if workflow_steps is not UNSET:
            field_dict["workflow_steps"] = workflow_steps
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if subject is not UNSET:
            field_dict["subject"] = subject

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_step import OperationWorkflowStep

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        operation_id = UUID(d.pop("operation_id"))

        name = d.pop("name")

        payload = d.pop("payload")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        sequence = d.pop("sequence")

        _workflow_steps = d.pop("workflow_steps", UNSET)
        workflow_steps: list[OperationWorkflowStep] | Unset = UNSET
        if _workflow_steps is not UNSET:
            workflow_steps = []
            for workflow_steps_item_data in _workflow_steps:
                workflow_steps_item = OperationWorkflowStep.from_dict(workflow_steps_item_data)

                workflow_steps.append(workflow_steps_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _subject = d.pop("subject", UNSET)
        subject: OperationSubject | Unset
        if isinstance(_subject, Unset):
            subject = UNSET
        else:
            subject = OperationSubject.from_dict(_subject)

        operation_milestone = cls(
            id=id,
            operation_id=operation_id,
            name=name,
            payload=payload,
            occurred_at=occurred_at,
            created_at=created_at,
            sequence=sequence,
            workflow_steps=workflow_steps,
            platform_tenant_id=platform_tenant_id,
            subject=subject,
        )

        return operation_milestone
