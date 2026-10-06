from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_receipt_recipient_response_execution_unavailable import (
    EventReceiptRecipientResponseExecutionUnavailable,
    check_event_receipt_recipient_response_execution_unavailable,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_receipt_cancellation_response import EventReceiptCancellationResponse
    from ..models.event_receipt_execution_response import EventReceiptExecutionResponse
    from ..models.event_receipt_recovery_action import EventReceiptRecoveryAction
    from ..models.event_receipt_recovery_response import EventReceiptRecoveryResponse
    from ..models.event_receipt_routing_response import EventReceiptRoutingResponse


T = TypeVar("T", bound="EventReceiptRecipientResponse")


@_attrs_define
class EventReceiptRecipientResponse:
    """One captured recipient with independent routing, original execution or cancellation evidence, and retained generic
    replay recovery.

    """

    subscription_id: str
    """Immutable captured subscription or notification identifier."""
    app_id: UUID
    routing: EventReceiptRoutingResponse
    """Recipient routing checkpoint and lifetime attempts; execution attempts are separate."""
    recovery_actions: list[EventReceiptRecoveryAction]
    """Applicable selective recovery requests; empty when no action is currently eligible. Authorization and state
    are checked again on POST."""
    app_slug: str | Unset = UNSET
    """Current slug when the target still belongs to the authenticated account."""
    execution: EventReceiptExecutionResponse | Unset = UNSET
    """Current retained invocation state. The recipient execution is the original deterministic invocation;
    recovery and history contain trusted generic replay invocations."""
    recovery: EventReceiptRecoveryResponse | Unset = UNSET
    """Latest retained generic replay and history; original execution remains separate. Completed means this latest
    replay recovered; counts may decrease as execution records expire. Absence does not prove that no historical
    replay occurred."""
    cancellation: EventReceiptCancellationResponse | Unset = UNSET
    """Durable work-policy cancel_pending operation receipt, which creates no handler invocation."""
    execution_unavailable: EventReceiptRecipientResponseExecutionUnavailable | Unset = UNSET
    """No original invocation was found; record_unavailable after enqueue may reflect independent retention and is
    not a success claim."""
    fanout_history_url: str | Unset = UNSET
    """Account-authenticated routing and replay history for this recipient."""
    attempt_history_url: str | Unset = UNSET
    """Account-authenticated handler attempt history including trusted replays."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = self.subscription_id

        app_id = str(self.app_id)

        routing = self.routing.to_dict()

        recovery_actions = []
        for recovery_actions_item_data in self.recovery_actions:
            recovery_actions_item = recovery_actions_item_data.to_dict()
            recovery_actions.append(recovery_actions_item)

        app_slug = self.app_slug

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        cancellation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.cancellation, Unset):
            cancellation = self.cancellation.to_dict()

        execution_unavailable: str | Unset = UNSET
        if not isinstance(self.execution_unavailable, Unset):
            execution_unavailable = self.execution_unavailable

        fanout_history_url = self.fanout_history_url

        attempt_history_url = self.attempt_history_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "app_id": app_id,
                "routing": routing,
                "recovery_actions": recovery_actions,
            }
        )
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if execution is not UNSET:
            field_dict["execution"] = execution
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if cancellation is not UNSET:
            field_dict["cancellation"] = cancellation
        if execution_unavailable is not UNSET:
            field_dict["execution_unavailable"] = execution_unavailable
        if fanout_history_url is not UNSET:
            field_dict["fanout_history_url"] = fanout_history_url
        if attempt_history_url is not UNSET:
            field_dict["attempt_history_url"] = attempt_history_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_receipt_cancellation_response import EventReceiptCancellationResponse
        from ..models.event_receipt_execution_response import EventReceiptExecutionResponse
        from ..models.event_receipt_recovery_action import EventReceiptRecoveryAction
        from ..models.event_receipt_recovery_response import EventReceiptRecoveryResponse
        from ..models.event_receipt_routing_response import EventReceiptRoutingResponse

        d = dict(src_dict)
        subscription_id = d.pop("subscription_id")

        app_id = UUID(d.pop("app_id"))

        routing = EventReceiptRoutingResponse.from_dict(d.pop("routing"))

        recovery_actions = []
        _recovery_actions = d.pop("recovery_actions")
        for recovery_actions_item_data in _recovery_actions:
            recovery_actions_item = EventReceiptRecoveryAction.from_dict(recovery_actions_item_data)

            recovery_actions.append(recovery_actions_item)

        app_slug = d.pop("app_slug", UNSET)

        _execution = d.pop("execution", UNSET)
        execution: EventReceiptExecutionResponse | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = EventReceiptExecutionResponse.from_dict(_execution)

        _recovery = d.pop("recovery", UNSET)
        recovery: EventReceiptRecoveryResponse | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = EventReceiptRecoveryResponse.from_dict(_recovery)

        _cancellation = d.pop("cancellation", UNSET)
        cancellation: EventReceiptCancellationResponse | Unset
        if isinstance(_cancellation, Unset):
            cancellation = UNSET
        else:
            cancellation = EventReceiptCancellationResponse.from_dict(_cancellation)

        _execution_unavailable = d.pop("execution_unavailable", UNSET)
        execution_unavailable: EventReceiptRecipientResponseExecutionUnavailable | Unset
        if isinstance(_execution_unavailable, Unset):
            execution_unavailable = UNSET
        else:
            execution_unavailable = check_event_receipt_recipient_response_execution_unavailable(_execution_unavailable)

        fanout_history_url = d.pop("fanout_history_url", UNSET)

        attempt_history_url = d.pop("attempt_history_url", UNSET)

        event_receipt_recipient_response = cls(
            subscription_id=subscription_id,
            app_id=app_id,
            routing=routing,
            recovery_actions=recovery_actions,
            app_slug=app_slug,
            execution=execution,
            recovery=recovery,
            cancellation=cancellation,
            execution_unavailable=execution_unavailable,
            fanout_history_url=fanout_history_url,
            attempt_history_url=attempt_history_url,
        )

        return event_receipt_recipient_response
