from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject


T = TypeVar("T", bound="OperationWorkflowReviewResponseLink")


@_attrs_define
class OperationWorkflowReviewResponseLink:
    """OperationWorkflowReviewResponseLink. Immutable source identity captured from an authorized matching review delivery;
    preserved after inbox retention loss.

    """

    subscription_id: UUID
    """OperationWorkflowReviewResponseLink.subscription_id. Retained review subscription identity."""
    sequence: int
    """OperationWorkflowReviewResponseLink.sequence. Original delivery sequence."""
    notification_id: UUID
    """OperationWorkflowReviewResponseLink.notification_id. Original delivery identity, captured by the server."""
    progress_id: UUID
    """OperationWorkflowReviewResponseLink.progress_id. Pinned source playbook progress identity."""
    outcome_code: str
    """OperationWorkflowReviewResponseLink.outcome_code. Pinned follow-up outcome list."""
    item_index: int
    """OperationWorkflowReviewResponseLink.item_index. Pinned local step index."""
    dependency_review_revision: int
    """OperationWorkflowReviewResponseLink.dependency_review_revision. Original review obligation revision."""
    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    workflow: str
    """OperationWorkflowReviewResponseLink.workflow. Protected workflow identity matching the parent risk alert."""
    instance_id: str
    """OperationWorkflowReviewResponseLink.instance_id. Scoped source workflow instance."""
    platform_tenant_id: UUID | Unset = UNSET
    """OperationWorkflowReviewResponseLink.platform_tenant_id. Account-only exact source customer identity;
    customer responses omit this field."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        notification_id = str(self.notification_id)

        progress_id = str(self.progress_id)

        outcome_code = self.outcome_code

        item_index = self.item_index

        dependency_review_revision = self.dependency_review_revision

        subject = self.subject.to_dict()

        workflow = self.workflow

        instance_id = self.instance_id

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "sequence": sequence,
                "notification_id": notification_id,
                "progress_id": progress_id,
                "outcome_code": outcome_code,
                "item_index": item_index,
                "dependency_review_revision": dependency_review_revision,
                "subject": subject,
                "workflow": workflow,
                "instance_id": instance_id,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        notification_id = UUID(d.pop("notification_id"))

        progress_id = UUID(d.pop("progress_id"))

        outcome_code = d.pop("outcome_code")

        item_index = d.pop("item_index")

        dependency_review_revision = d.pop("dependency_review_revision")

        subject = OperationSubject.from_dict(d.pop("subject"))

        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_review_response_link = cls(
            subscription_id=subscription_id,
            sequence=sequence,
            notification_id=notification_id,
            progress_id=progress_id,
            outcome_code=outcome_code,
            item_index=item_index,
            dependency_review_revision=dependency_review_revision,
            subject=subject,
            workflow=workflow,
            instance_id=instance_id,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_review_response_link
