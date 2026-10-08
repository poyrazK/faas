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
    from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowAttentionEntry")


@_attrs_define
class OperationWorkflowAttentionEntry:
    app_id: UUID
    scope: str
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    operation_id: UUID
    """Operation that published the current state report."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    reasons: list[OperationWorkflowAttentionEntryReasonsItem]
    dependency_attention: list[OperationWorkflowRelatedInstance] | Unset = UNSET
    """Unresolved direct references on an active source. These entries carry dependency and status; target state is
    available in the workflow instance detail."""
    platform_tenant_id: UUID | Unset = UNSET
    """Present only in account-operator responses."""

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
        if dependency_attention is not UNSET:
            field_dict["dependency_attention"] = dependency_attention
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
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
            dependency_attention=dependency_attention,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_attention_entry
