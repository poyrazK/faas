from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookEvidence")


@_attrs_define
class OperationWorkflowPlaybookEvidence:
    """OperationWorkflowPlaybookEvidence. Durable advisory checklist coordination, independent of application business
    success.

    """

    operation_id: UUID
    """OperationWorkflowPlaybookEvidence.operation_id. Operation checked against exact customer, application,
    environment and business subject."""
    milestone_id: str | Unset = UNSET
    """OperationWorkflowPlaybookEvidence.milestone_id. Optional retained fact additionally checked against pinned
    workflow, instance and contract version."""

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        milestone_id = self.milestone_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "operation_id": operation_id,
            }
        )
        if milestone_id is not UNSET:
            field_dict["milestone_id"] = milestone_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        milestone_id = d.pop("milestone_id", UNSET)

        operation_workflow_playbook_evidence = cls(
            operation_id=operation_id,
            milestone_id=milestone_id,
        )

        return operation_workflow_playbook_evidence
