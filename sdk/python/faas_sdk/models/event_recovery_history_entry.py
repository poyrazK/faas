from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_history_entry_action import (
    EventRecoveryHistoryEntryAction,
    check_event_recovery_history_entry_action,
)
from ..models.event_recovery_history_entry_actor_kind import (
    EventRecoveryHistoryEntryActorKind,
    check_event_recovery_history_entry_actor_kind,
)
from ..models.event_recovery_history_entry_previous_state import (
    EventRecoveryHistoryEntryPreviousState,
    check_event_recovery_history_entry_previous_state,
)
from ..models.event_recovery_history_entry_state import (
    EventRecoveryHistoryEntryState,
    check_event_recovery_history_entry_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryHistoryEntry")


@_attrs_define
class EventRecoveryHistoryEntry:
    id: int
    occurred_at: datetime.datetime
    action: EventRecoveryHistoryEntryAction
    actor_kind: EventRecoveryHistoryEntryActorKind
    actor_id: str
    """Authenticated account or API key ID, or an internal/system identifier. No credential values."""
    previous_state: EventRecoveryHistoryEntryPreviousState
    state: EventRecoveryHistoryEntryState
    previous_rate: int
    rate: int
    reason: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        occurred_at = self.occurred_at.isoformat()

        action: str = self.action

        actor_kind: str = self.actor_kind

        actor_id = self.actor_id

        previous_state: str = self.previous_state

        state: str = self.state

        previous_rate = self.previous_rate

        rate = self.rate

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "occurred_at": occurred_at,
                "action": action,
                "actor_kind": actor_kind,
                "actor_id": actor_id,
                "previous_state": previous_state,
                "state": state,
                "previous_rate": previous_rate,
                "rate": rate,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        action = check_event_recovery_history_entry_action(d.pop("action"))

        actor_kind = check_event_recovery_history_entry_actor_kind(d.pop("actor_kind"))

        actor_id = d.pop("actor_id")

        previous_state = check_event_recovery_history_entry_previous_state(d.pop("previous_state"))

        state = check_event_recovery_history_entry_state(d.pop("state"))

        previous_rate = d.pop("previous_rate")

        rate = d.pop("rate")

        reason = d.pop("reason", UNSET)

        event_recovery_history_entry = cls(
            id=id,
            occurred_at=occurred_at,
            action=action,
            actor_kind=actor_kind,
            actor_id=actor_id,
            previous_state=previous_state,
            state=state,
            previous_rate=previous_rate,
            rate=rate,
            reason=reason,
        )

        return event_recovery_history_entry
