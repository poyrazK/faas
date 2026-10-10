from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowPerformanceInstance")


@_attrs_define
class OperationWorkflowPerformanceInstance:
    """Current state and verification findings for one complete-history contributor. Customer responses omit tenant
    identifiers. Observed seconds measure the selected historical group rather than current ownership alone.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    operation_id: UUID
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    observed_seconds: int
    resolution_verifications: list[OperationWorkflowResolutionVerification]
    awaiting_verification_count: int
    resolution_verification_count: int
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        operation_id = str(self.operation_id)

        state = self.state.to_dict()

        observed_seconds = self.observed_seconds

        resolution_verifications = []
        for resolution_verifications_item_data in self.resolution_verifications:
            resolution_verifications_item = resolution_verifications_item_data.to_dict()
            resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = self.awaiting_verification_count

        resolution_verification_count = self.resolution_verification_count

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "operation_id": operation_id,
                "state": state,
                "observed_seconds": observed_seconds,
                "resolution_verifications": resolution_verifications,
                "awaiting_verification_count": awaiting_verification_count,
                "resolution_verification_count": resolution_verification_count,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        operation_id = UUID(d.pop("operation_id"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        observed_seconds = d.pop("observed_seconds")

        resolution_verifications = []
        _resolution_verifications = d.pop("resolution_verifications")
        for resolution_verifications_item_data in _resolution_verifications:
            resolution_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                resolution_verifications_item_data
            )

            resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = d.pop("awaiting_verification_count")

        resolution_verification_count = d.pop("resolution_verification_count")

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_performance_instance = cls(
            subject=subject,
            operation_id=operation_id,
            state=state,
            observed_seconds=observed_seconds,
            resolution_verifications=resolution_verifications,
            awaiting_verification_count=awaiting_verification_count,
            resolution_verification_count=resolution_verification_count,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_performance_instance
