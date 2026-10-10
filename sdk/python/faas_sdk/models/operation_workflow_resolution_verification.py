from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_resolution_verification_status import (
    OperationWorkflowResolutionVerificationStatus,
    check_operation_workflow_resolution_verification_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution


T = TypeVar("T", bound="OperationWorkflowResolutionVerification")


@_attrs_define
class OperationWorkflowResolutionVerification:
    """Retained resolution claim and its exact proof-verification status under the original scoped workflow contract."""

    resolution: OperationWorkflowBlockerResolution
    """Explicit application explanation for clearing one prior blocker occurrence. The source must be a retained
    report in the same owner/business-reference/workflow-instance/contract-version boundary and must contain this
    target/code. The containing state report supplies the resolution identity and timestamps. A cleared list alone
    does not imply a resolution fact."""
    resolution_operation_id: UUID
    resolution_report_id: UUID
    resolution_revision: int
    status: OperationWorkflowResolutionVerificationStatus
    verified_at: datetime.datetime | Unset = UNSET
    """Retained proof publication time."""

    def to_dict(self) -> dict[str, Any]:
        resolution = self.resolution.to_dict()

        resolution_operation_id = str(self.resolution_operation_id)

        resolution_report_id = str(self.resolution_report_id)

        resolution_revision = self.resolution_revision

        status: str = self.status

        verified_at: str | Unset = UNSET
        if not isinstance(self.verified_at, Unset):
            verified_at = self.verified_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "resolution": resolution,
                "resolution_operation_id": resolution_operation_id,
                "resolution_report_id": resolution_report_id,
                "resolution_revision": resolution_revision,
                "status": status,
            }
        )
        if verified_at is not UNSET:
            field_dict["verified_at"] = verified_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker_resolution import OperationWorkflowBlockerResolution

        d = dict(src_dict)
        resolution = OperationWorkflowBlockerResolution.from_dict(d.pop("resolution"))

        resolution_operation_id = UUID(d.pop("resolution_operation_id"))

        resolution_report_id = UUID(d.pop("resolution_report_id"))

        resolution_revision = d.pop("resolution_revision")

        status = check_operation_workflow_resolution_verification_status(d.pop("status"))

        _verified_at = d.pop("verified_at", UNSET)
        verified_at: datetime.datetime | Unset
        if isinstance(_verified_at, Unset):
            verified_at = UNSET
        else:
            verified_at = datetime.datetime.fromisoformat(_verified_at)

        operation_workflow_resolution_verification = cls(
            resolution=resolution,
            resolution_operation_id=resolution_operation_id,
            resolution_report_id=resolution_report_id,
            resolution_revision=resolution_revision,
            status=status,
            verified_at=verified_at,
        )

        return operation_workflow_resolution_verification
