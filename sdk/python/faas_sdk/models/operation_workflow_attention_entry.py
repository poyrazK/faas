from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_attention_entry_reasons_item import (
    OperationWorkflowAttentionEntryReasonsItem,
    check_operation_workflow_attention_entry_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_blocker_escalation import OperationWorkflowBlockerEscalation
    from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowAttentionEntry")


@_attrs_define
class OperationWorkflowAttentionEntry:
    """One active workflow instance requiring attention, including its reasons and unresolved prerequisites."""

    app_id: UUID
    scope: str
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    operation_id: UUID
    """For this attention entry, operation that published the current state report."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    reasons: list[OperationWorkflowAttentionEntryReasonsItem]
    resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
    """Bounded preview with pending obligations first. Exact counts cover all retained distinct obligations."""
    awaiting_verification_count: int | Unset = UNSET
    resolution_verification_count: int | Unset = UNSET
    escalations: list[OperationWorkflowBlockerEscalation] | Unset = UNSET
    """Passed blocker escalation thresholds; missing observation times do not produce findings."""
    dependency_attention: list[OperationWorkflowRelatedInstance] | Unset = UNSET
    """Unresolved direct references on an active source. These entries carry dependency and status; target state is
    available in the workflow instance detail."""
    platform_tenant_id: UUID | Unset = UNSET
    """For this attention entry, present only in account-operator responses."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        subject = self.subject.to_dict()

        operation_id = str(self.operation_id)

        state = self.state.to_dict()

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        resolution_verifications: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.resolution_verifications, Unset):
            resolution_verifications = []
            for resolution_verifications_item_data in self.resolution_verifications:
                resolution_verifications_item = resolution_verifications_item_data.to_dict()
                resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = self.awaiting_verification_count

        resolution_verification_count = self.resolution_verification_count

        escalations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.escalations, Unset):
            escalations = []
            for escalations_item_data in self.escalations:
                escalations_item = escalations_item_data.to_dict()
                escalations.append(escalations_item)

        dependency_attention: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.dependency_attention, Unset):
            dependency_attention = []
            for dependency_attention_item_data in self.dependency_attention:
                dependency_attention_item = dependency_attention_item_data.to_dict()
                dependency_attention.append(dependency_attention_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "subject": subject,
                "operation_id": operation_id,
                "state": state,
                "reasons": reasons,
            }
        )
        if resolution_verifications is not UNSET:
            field_dict["resolution_verifications"] = resolution_verifications
        if awaiting_verification_count is not UNSET:
            field_dict["awaiting_verification_count"] = awaiting_verification_count
        if resolution_verification_count is not UNSET:
            field_dict["resolution_verification_count"] = resolution_verification_count
        if escalations is not UNSET:
            field_dict["escalations"] = escalations
        if dependency_attention is not UNSET:
            field_dict["dependency_attention"] = dependency_attention
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_blocker_escalation import OperationWorkflowBlockerEscalation
        from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        subject = OperationSubject.from_dict(d.pop("subject"))

        operation_id = UUID(d.pop("operation_id"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_operation_workflow_attention_entry_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        _resolution_verifications = d.pop("resolution_verifications", UNSET)
        resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
        if _resolution_verifications is not UNSET:
            resolution_verifications = []
            for resolution_verifications_item_data in _resolution_verifications:
                resolution_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                    resolution_verifications_item_data
                )

                resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = d.pop("awaiting_verification_count", UNSET)

        resolution_verification_count = d.pop("resolution_verification_count", UNSET)

        _escalations = d.pop("escalations", UNSET)
        escalations: list[OperationWorkflowBlockerEscalation] | Unset = UNSET
        if _escalations is not UNSET:
            escalations = []
            for escalations_item_data in _escalations:
                escalations_item = OperationWorkflowBlockerEscalation.from_dict(escalations_item_data)

                escalations.append(escalations_item)

        _dependency_attention = d.pop("dependency_attention", UNSET)
        dependency_attention: list[OperationWorkflowRelatedInstance] | Unset = UNSET
        if _dependency_attention is not UNSET:
            dependency_attention = []
            for dependency_attention_item_data in _dependency_attention:
                dependency_attention_item = OperationWorkflowRelatedInstance.from_dict(dependency_attention_item_data)

                dependency_attention.append(dependency_attention_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_attention_entry = cls(
            app_id=app_id,
            scope=scope,
            subject=subject,
            operation_id=operation_id,
            state=state,
            reasons=reasons,
            resolution_verifications=resolution_verifications,
            awaiting_verification_count=awaiting_verification_count,
            resolution_verification_count=resolution_verification_count,
            escalations=escalations,
            dependency_attention=dependency_attention,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_attention_entry
