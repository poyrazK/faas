from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
    from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
    from ..models.operation_workflow_obligation_fulfillment_review import OperationWorkflowObligationFulfillmentReview


T = TypeVar("T", bound="OperationWorkflowCaseObligationHistoryItem")


@_attrs_define
class OperationWorkflowCaseObligationHistoryItem:
    """Case-scoped receipts grouped by exact source obligation, alongside its current action when still projected. Absence
    of a current action is not completion. Shared source controls can affect additional cases; foreign case identities
    are excluded.

    """

    obligation_id: str
    action_kind: str
    subscription_id: UUID
    sequence: int
    scope: str
    shared_plan: bool
    events: list[OperationWorkflowObligationEvent]
    events_truncated: bool
    fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset = UNSET
    """Bounded independent observation of the latest fulfilled case/cycle and its original or reaffirmed proof.
    Flags remain latched until explicit reaffirmation or reopening; normal workflow progress is not invalidation.
    Reasons describe retained coverage, not inferred business failure. Identical semantic observations suppress
    owner notices."""
    subscription_tenant_id: UUID | Unset = UNSET
    current: OperationWorkflowCaseNextAction | Unset = UNSET
    """One current coordination obligation or suggestion for an exact workflow case, including the responsible
    label, its own deadline and a source inbox link. Shared plans may span cases; evidence includes only references
    matching this case. This does not authorize application execution or infer business completion."""

    def to_dict(self) -> dict[str, Any]:
        obligation_id = self.obligation_id

        action_kind = self.action_kind

        subscription_id = str(self.subscription_id)

        sequence = self.sequence

        scope = self.scope

        shared_plan = self.shared_plan

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        events_truncated = self.events_truncated

        fulfillment_review: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment_review, Unset):
            fulfillment_review = self.fulfillment_review.to_dict()

        subscription_tenant_id: str | Unset = UNSET
        if not isinstance(self.subscription_tenant_id, Unset):
            subscription_tenant_id = str(self.subscription_tenant_id)

        current: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current, Unset):
            current = self.current.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "obligation_id": obligation_id,
                "action_kind": action_kind,
                "subscription_id": subscription_id,
                "sequence": sequence,
                "scope": scope,
                "shared_plan": shared_plan,
                "events": events,
                "events_truncated": events_truncated,
            }
        )
        if fulfillment_review is not UNSET:
            field_dict["fulfillment_review"] = fulfillment_review
        if subscription_tenant_id is not UNSET:
            field_dict["subscription_tenant_id"] = subscription_tenant_id
        if current is not UNSET:
            field_dict["current"] = current

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
        from ..models.operation_workflow_obligation_event import OperationWorkflowObligationEvent
        from ..models.operation_workflow_obligation_fulfillment_review import (
            OperationWorkflowObligationFulfillmentReview,
        )

        d = dict(src_dict)
        obligation_id = d.pop("obligation_id")

        action_kind = d.pop("action_kind")

        subscription_id = UUID(d.pop("subscription_id"))

        sequence = d.pop("sequence")

        scope = d.pop("scope")

        shared_plan = d.pop("shared_plan")

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = OperationWorkflowObligationEvent.from_dict(events_item_data)

            events.append(events_item)

        events_truncated = d.pop("events_truncated")

        _fulfillment_review = d.pop("fulfillment_review", UNSET)
        fulfillment_review: OperationWorkflowObligationFulfillmentReview | Unset
        if isinstance(_fulfillment_review, Unset):
            fulfillment_review = UNSET
        else:
            fulfillment_review = OperationWorkflowObligationFulfillmentReview.from_dict(_fulfillment_review)

        _subscription_tenant_id = d.pop("subscription_tenant_id", UNSET)
        subscription_tenant_id: UUID | Unset
        if isinstance(_subscription_tenant_id, Unset):
            subscription_tenant_id = UNSET
        else:
            subscription_tenant_id = UUID(_subscription_tenant_id)

        _current = d.pop("current", UNSET)
        current: OperationWorkflowCaseNextAction | Unset
        if isinstance(_current, Unset):
            current = UNSET
        else:
            current = OperationWorkflowCaseNextAction.from_dict(_current)

        operation_workflow_case_obligation_history_item = cls(
            obligation_id=obligation_id,
            action_kind=action_kind,
            subscription_id=subscription_id,
            sequence=sequence,
            scope=scope,
            shared_plan=shared_plan,
            events=events,
            events_truncated=events_truncated,
            fulfillment_review=fulfillment_review,
            subscription_tenant_id=subscription_tenant_id,
            current=current,
        )

        return operation_workflow_case_obligation_history_item
