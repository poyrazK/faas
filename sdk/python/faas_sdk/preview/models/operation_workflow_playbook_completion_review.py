from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_completion_review_status import (
    OperationWorkflowPlaybookCompletionReviewStatus,
    check_operation_workflow_playbook_completion_review_status,
)

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome


T = TypeVar("T", bound="OperationWorkflowPlaybookCompletionReview")


@_attrs_define
class OperationWorkflowPlaybookCompletionReview:
    """OperationWorkflowPlaybookCompletionReview. Bounded retained post-completion monitoring; updates never reopen or
    rewrite the original closure.

    """

    id: UUID
    """OperationWorkflowPlaybookCompletionReview.id. Exact durable review identity."""
    revision: int
    """OperationWorkflowPlaybookCompletionReview.revision. Worker observation fence."""
    owner: str
    """OperationWorkflowPlaybookCompletionReview.owner. Coordination owner frozen at completion."""
    status: OperationWorkflowPlaybookCompletionReviewStatus
    """OperationWorkflowPlaybookCompletionReview.status. Monitoring lifetime; expired reviews no longer publish."""
    expires_at: datetime.datetime
    """OperationWorkflowPlaybookCompletionReview.expires_at. Original bounded window end from actual completion."""
    state_revision: int
    """OperationWorkflowPlaybookCompletionReview.state_revision. Highest comparable retained application revision."""
    current: OperationWorkflowPlaybookBusinessOutcome
    """OperationWorkflowPlaybookBusinessOutcome. Exact-case application business result compared with a frozen
    playbook expectation. Independent of human completion and evidence satisfaction; changed guidance never silently
    changes the expected outcome."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revision = self.revision

        owner = self.owner

        status: str = self.status

        expires_at = self.expires_at.isoformat()

        state_revision = self.state_revision

        current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "revision": revision,
                "owner": owner,
                "status": status,
                "expires_at": expires_at,
                "state_revision": state_revision,
                "current": current,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_business_outcome import OperationWorkflowPlaybookBusinessOutcome

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revision = d.pop("revision")

        owner = d.pop("owner")

        status = check_operation_workflow_playbook_completion_review_status(d.pop("status"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        state_revision = d.pop("state_revision")

        current = OperationWorkflowPlaybookBusinessOutcome.from_dict(d.pop("current"))

        operation_workflow_playbook_completion_review = cls(
            id=id,
            revision=revision,
            owner=owner,
            status=status,
            expires_at=expires_at,
            state_revision=state_revision,
            current=current,
        )

        return operation_workflow_playbook_completion_review
