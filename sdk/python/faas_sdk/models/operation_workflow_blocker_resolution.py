from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowBlockerResolution")


@_attrs_define
class OperationWorkflowBlockerResolution:
    """Explicit application explanation for clearing one prior blocker occurrence. The source must be a retained report in
    the same owner/business-reference/workflow-instance/contract-version boundary and must contain this target/code. The
    containing state report supplies the resolution identity and timestamps. A cleared list alone does not imply a
    resolution fact.

    """

    code: str
    operation: str
    description: str
    """Public UTF-8 explanation limited to 512 bytes without control characters."""
    blocker_operation_id: UUID
    blocker_report_id: UUID
    blocker_revision: int
    """Source revision must precede the resolution report revision."""
    verification_milestone_id: UUID | Unset = UNSET
    """Exact expected milestone ID paired with verification_milestone_name. Missing retained evidence leaves
    verification pending."""
    verification_milestone_name: str | Unset = UNSET
    """Expected milestone name paired with its exact ID."""
    verification_operation_id: UUID | Unset = UNSET
    """Optional evidence Operation ID. Defaults to the Operation containing this resolution. Evidence must match
    the same account/customer/app/scope/subject/workflow-instance/contract boundary."""
    verification_owner: str | Unset = UNSET
    """Public application-assigned verification owner limited to 128 UTF-8 bytes without control characters.
    Requires a verification milestone. Omitted means unassigned."""
    resolved_by: str | Unset = UNSET
    """Optional public application-reported resolver identifier or team, limited to 128 UTF-8 bytes without control
    characters. This is not verified platform identity."""

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        operation = self.operation

        description = self.description

        blocker_operation_id = str(self.blocker_operation_id)

        blocker_report_id = str(self.blocker_report_id)

        blocker_revision = self.blocker_revision

        verification_milestone_id: str | Unset = UNSET
        if not isinstance(self.verification_milestone_id, Unset):
            verification_milestone_id = str(self.verification_milestone_id)

        verification_milestone_name = self.verification_milestone_name

        verification_operation_id: str | Unset = UNSET
        if not isinstance(self.verification_operation_id, Unset):
            verification_operation_id = str(self.verification_operation_id)

        verification_owner = self.verification_owner

        resolved_by = self.resolved_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "operation": operation,
                "description": description,
                "blocker_operation_id": blocker_operation_id,
                "blocker_report_id": blocker_report_id,
                "blocker_revision": blocker_revision,
            }
        )
        if verification_milestone_id is not UNSET:
            field_dict["verification_milestone_id"] = verification_milestone_id
        if verification_milestone_name is not UNSET:
            field_dict["verification_milestone_name"] = verification_milestone_name
        if verification_operation_id is not UNSET:
            field_dict["verification_operation_id"] = verification_operation_id
        if verification_owner is not UNSET:
            field_dict["verification_owner"] = verification_owner
        if resolved_by is not UNSET:
            field_dict["resolved_by"] = resolved_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        operation = d.pop("operation")

        description = d.pop("description")

        blocker_operation_id = UUID(d.pop("blocker_operation_id"))

        blocker_report_id = UUID(d.pop("blocker_report_id"))

        blocker_revision = d.pop("blocker_revision")

        _verification_milestone_id = d.pop("verification_milestone_id", UNSET)
        verification_milestone_id: UUID | Unset
        if isinstance(_verification_milestone_id, Unset):
            verification_milestone_id = UNSET
        else:
            verification_milestone_id = UUID(_verification_milestone_id)

        verification_milestone_name = d.pop("verification_milestone_name", UNSET)

        _verification_operation_id = d.pop("verification_operation_id", UNSET)
        verification_operation_id: UUID | Unset
        if isinstance(_verification_operation_id, Unset):
            verification_operation_id = UNSET
        else:
            verification_operation_id = UUID(_verification_operation_id)

        verification_owner = d.pop("verification_owner", UNSET)

        resolved_by = d.pop("resolved_by", UNSET)

        operation_workflow_blocker_resolution = cls(
            code=code,
            operation=operation,
            description=description,
            blocker_operation_id=blocker_operation_id,
            blocker_report_id=blocker_report_id,
            blocker_revision=blocker_revision,
            verification_milestone_id=verification_milestone_id,
            verification_milestone_name=verification_milestone_name,
            verification_operation_id=verification_operation_id,
            verification_owner=verification_owner,
            resolved_by=resolved_by,
        )

        return operation_workflow_blocker_resolution
