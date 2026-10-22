from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_digest_check import OperationWorkflowNextActionDigestCheck
    from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
    from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
    from ..models.operation_workflow_readiness_handoff import OperationWorkflowReadinessHandoff
    from ..models.operation_workflow_readiness_notification import OperationWorkflowReadinessNotification


T = TypeVar("T", bound="OperationWorkflowReadinessNotificationCheck")


@_attrs_define
class OperationWorkflowReadinessNotificationCheck:
    """Current advisory evidence check preserving the original notification. Global failures remain unavailable."""

    handoff: OperationWorkflowReadinessHandoff
    """Advisory handoff context for the same checked workflow observation; scheduling-time evidence is not
    retained."""
    notification: OperationWorkflowReadinessNotification
    """Durable advisory readiness notification containing a bounded comparison and reporting Operation identities."""
    checked_at: datetime.datetime
    """OperationWorkflowReadinessNotificationCheck.checked_at. Current check time."""
    status: str
    """OperationWorkflowReadinessNotificationCheck.status. Digest_checked for per-item source reconciliation; ready
    or blocked only for a comparable retained edge; otherwise the explicit comparison boundary status."""
    next_action_digest: OperationWorkflowNextActionDigestCheck | Unset = UNSET
    """Read-only current source reconciliation of a saved digest, using independent retained source reads. No
    delivery snapshot or workflow is modified."""
    business_outcome: OperationWorkflowReadinessBusinessOutcome | Unset = UNSET
    """Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing
    state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain
    unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are
    immutable; current checks are live reads."""
    change: OperationWorkflowReadinessChange | Unset = UNSET
    """Readiness comparison of the saved edge with fresh retained evidence. Contract and state boundaries are
    explicit."""

    def to_dict(self) -> dict[str, Any]:
        handoff = self.handoff.to_dict()

        notification = self.notification.to_dict()

        checked_at = self.checked_at.isoformat()

        status = self.status

        next_action_digest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.next_action_digest, Unset):
            next_action_digest = self.next_action_digest.to_dict()

        business_outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.business_outcome, Unset):
            business_outcome = self.business_outcome.to_dict()

        change: dict[str, Any] | Unset = UNSET
        if not isinstance(self.change, Unset):
            change = self.change.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "handoff": handoff,
                "notification": notification,
                "checked_at": checked_at,
                "status": status,
            }
        )
        if next_action_digest is not UNSET:
            field_dict["next_action_digest"] = next_action_digest
        if business_outcome is not UNSET:
            field_dict["business_outcome"] = business_outcome
        if change is not UNSET:
            field_dict["change"] = change

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_digest_check import OperationWorkflowNextActionDigestCheck
        from ..models.operation_workflow_readiness_business_outcome import OperationWorkflowReadinessBusinessOutcome
        from ..models.operation_workflow_readiness_change import OperationWorkflowReadinessChange
        from ..models.operation_workflow_readiness_handoff import OperationWorkflowReadinessHandoff
        from ..models.operation_workflow_readiness_notification import OperationWorkflowReadinessNotification

        d = dict(src_dict)
        handoff = OperationWorkflowReadinessHandoff.from_dict(d.pop("handoff"))

        notification = OperationWorkflowReadinessNotification.from_dict(d.pop("notification"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = d.pop("status")

        _next_action_digest = d.pop("next_action_digest", UNSET)
        next_action_digest: OperationWorkflowNextActionDigestCheck | Unset
        if isinstance(_next_action_digest, Unset):
            next_action_digest = UNSET
        else:
            next_action_digest = OperationWorkflowNextActionDigestCheck.from_dict(_next_action_digest)

        _business_outcome = d.pop("business_outcome", UNSET)
        business_outcome: OperationWorkflowReadinessBusinessOutcome | Unset
        if isinstance(_business_outcome, Unset):
            business_outcome = UNSET
        else:
            business_outcome = OperationWorkflowReadinessBusinessOutcome.from_dict(_business_outcome)

        _change = d.pop("change", UNSET)
        change: OperationWorkflowReadinessChange | Unset
        if isinstance(_change, Unset):
            change = UNSET
        else:
            change = OperationWorkflowReadinessChange.from_dict(_change)

        operation_workflow_readiness_notification_check = cls(
            handoff=handoff,
            notification=notification,
            checked_at=checked_at,
            status=status,
            next_action_digest=next_action_digest,
            business_outcome=business_outcome,
            change=change,
        )

        return operation_workflow_readiness_notification_check
