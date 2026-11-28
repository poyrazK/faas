from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.operation_workflow_next_action_digest_check_item_status import (
    OperationWorkflowNextActionDigestCheckItemStatus,
    check_operation_workflow_next_action_digest_check_item_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem
    from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
    from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion


T = TypeVar("T", bound="OperationWorkflowNextActionDigestCheckItem")


@_attrs_define
class OperationWorkflowNextActionDigestCheckItem:
    """Current result for one saved source action. Completed requires an epoch-matched lifecycle receipt or an explicit
    resolved generic source follow-up closure; no-longer-outstanding and unavailable never imply completion. Current
    owner filters are intentionally not applied so reassignment remains visible. Linked evidence health and application
    success are not inferred.

    """

    history_limited: bool
    """Lifecycle coverage is bounded by source retention and available anchored receipts; missing events never
    prove completion."""
    subscription_id: UUID
    sequence: int
    action_kind: str
    status: OperationWorkflowNextActionDigestCheckItemStatus
    explanation: str
    changed_fields: list[str]
    obligation_id: str | Unset = UNSET
    lifecycle: list[OperationWorkflowObligationEvent] | Unset = UNSET
    current: OperationWorkflowNextActionQueueItem | Unset = UNSET
    """Current source action shown once across its matching workflow cases. Owner and due filters apply to the
    action, not parent coordination. Shared record controls affect all covered cases; evidence and case lists
    respect the customer/workflow selector."""
    completion: OperationWorkflowReadinessCompletion | Unset = UNSET
    """Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write
    evidence check. Retained with the original notification."""

    def to_dict(self) -> dict[str, Any]:
        history_limited = self.history_limited

        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        action_kind = self.action_kind

        status: str = self.status

        explanation = self.explanation

        changed_fields = self.changed_fields

        obligation_id = self.obligation_id

        lifecycle: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.lifecycle, Unset):
            lifecycle = []
            for lifecycle_item_data in self.lifecycle:
                lifecycle_item = lifecycle_item_data.to_dict()
                lifecycle.append(lifecycle_item)

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "history_limited": history_limited,
                "subscription_id": subscription_id,
                "sequence": sequence,
                "action_kind": action_kind,
                "status": status,
                "explanation": explanation,
                "changed_fields": changed_fields,
            }
        )
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id
        if lifecycle is not UNSET:
            field_dict["lifecycle"] = lifecycle
        if current is not UNSET:
            field_dict["current"] = current
        if completion is not UNSET:
            field_dict["completion"] = completion

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_queue_item import OperationWorkflowNextActionQueueItem
        from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
        from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion

        d = dict(src_dict)
        history_limited = d.pop("history_limited")

        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        action_kind = d.pop("action_kind")

        status = check_operation_workflow_next_action_digest_check_item_status(d.pop("status"))

        explanation = d.pop("explanation")

        changed_fields = cast(list[str], d.pop("changed_fields"))

        obligation_id = d.pop("obligation_id", UNSET)

        _lifecycle = d.pop("lifecycle", UNSET)
        lifecycle: list[OperationWorkflowObligationEvent] | Unset = UNSET
        if _lifecycle is not UNSET:
            lifecycle = []
            for lifecycle_item_data in _lifecycle:
                lifecycle_item = OperationWorkflowObligationEvent.from_dict(lifecycle_item_data)

                lifecycle.append(lifecycle_item)

        _current = d.pop("current", UNSET)
        current: OperationWorkflowNextActionQueueItem | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = OperationWorkflowNextActionQueueItem.from_dict(_current)

        _completion = d.pop("completion", UNSET)
        completion: OperationWorkflowReadinessCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = OperationWorkflowReadinessCompletion.from_dict(_completion)

        operation_workflow_next_action_digest_check_item = cls(
            history_limited=history_limited,
            subscription_id=subscription_id,
            sequence=sequence,
            action_kind=action_kind,
            status=status,
            explanation=explanation,
            changed_fields=changed_fields,
            obligation_id=obligation_id,
            lifecycle=lifecycle,
            current=current,
            completion=completion,
        )

        return operation_workflow_next_action_digest_check_item
