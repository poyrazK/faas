from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_recovery_item_reason import EventRecoveryItemReason, check_event_recovery_item_reason
from ..models.event_recovery_item_state import EventRecoveryItemState, check_event_recovery_item_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_execution import EventRecoveryExecution


T = TypeVar("T", bound="EventRecoveryItem")


@_attrs_define
class EventRecoveryItem:
    """Metadata-only selected recipient. In execution mode invocation_id identifies the selected failure. Queued means
    replay was admitted, not that the handler succeeded.

    """

    position: int
    event_source: str
    event_id: str
    event_type: str
    subscription_id: str
    failed_at: datetime.datetime
    failure_code: str
    retryable: bool
    state: EventRecoveryItemState
    invocation_id: UUID | Unset = UNSET
    replay_invocation_id: UUID | Unset = UNSET
    """Exact replay admitted by this job; omitted for routing recovery and legacy items."""
    replay_generation: int | Unset = UNSET
    """Frozen admitted generation including zero for new replay children."""
    execution: EventRecoveryExecution | Unset = UNSET
    """Read-only observation of this job's admitted replay generation. Later replays are not attributed to this
    item. Missing history, untracked legacy items, or uncertain outcomes report unknown. Omitted for routing
    recovery and items not admitted."""
    reason: EventRecoveryItemReason | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        event_source = self.event_source

        event_id = self.event_id

        event_type = self.event_type

        subscription_id = self.subscription_id

        failed_at = self.failed_at.isoformat()

        failure_code = self.failure_code

        retryable = self.retryable

        state: str = self.state

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        replay_invocation_id: str | Unset = UNSET
        if not isinstance(self.replay_invocation_id, Unset):
            replay_invocation_id = str(self.replay_invocation_id)

        replay_generation = self.replay_generation

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "position": position,
                "event_source": event_source,
                "event_id": event_id,
                "event_type": event_type,
                "subscription_id": subscription_id,
                "failed_at": failed_at,
                "failure_code": failure_code,
                "retryable": retryable,
                "state": state,
            }
        )
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if replay_invocation_id is not UNSET:
            field_dict["replay_invocation_id"] = replay_invocation_id
        if replay_generation is not UNSET:
            field_dict["replay_generation"] = replay_generation
        if execution is not UNSET:
            field_dict["execution"] = execution
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_execution import EventRecoveryExecution

        d = dict(src_dict)
        position = d.pop("position")

        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        event_type = d.pop("event_type")

        subscription_id = d.pop("subscription_id")

        failed_at = datetime.datetime.fromisoformat(d.pop("failed_at"))

        failure_code = d.pop("failure_code")

        retryable = d.pop("retryable")

        state = check_event_recovery_item_state(d.pop("state"))

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _replay_invocation_id = d.pop("replay_invocation_id", UNSET)
        replay_invocation_id: UUID | Unset
        if isinstance(_replay_invocation_id, Unset):
            replay_invocation_id = UNSET
        else:
            replay_invocation_id = UUID(_replay_invocation_id)

        replay_generation = d.pop("replay_generation", UNSET)

        _execution = d.pop("execution", UNSET)
        execution: EventRecoveryExecution | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = EventRecoveryExecution.from_dict(_execution)

        _reason = d.pop("reason", UNSET)
        reason: EventRecoveryItemReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_event_recovery_item_reason(_reason)

        event_recovery_item = cls(
            position=position,
            event_source=event_source,
            event_id=event_id,
            event_type=event_type,
            subscription_id=subscription_id,
            failed_at=failed_at,
            failure_code=failure_code,
            retryable=retryable,
            state=state,
            invocation_id=invocation_id,
            replay_invocation_id=replay_invocation_id,
            replay_generation=replay_generation,
            execution=execution,
            reason=reason,
        )

        return event_recovery_item
