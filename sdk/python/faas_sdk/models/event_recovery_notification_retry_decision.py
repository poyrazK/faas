from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_retry_decision_retry_outcome import (
    EventRecoveryNotificationRetryDecisionRetryOutcome,
    check_event_recovery_notification_retry_decision_retry_outcome,
)
from ..models.event_recovery_notification_retry_decision_state import (
    EventRecoveryNotificationRetryDecisionState,
    check_event_recovery_notification_retry_decision_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget


T = TypeVar("T", bound="EventRecoveryNotificationRetryDecision")


@_attrs_define
class EventRecoveryNotificationRetryDecision:
    """Immutable original receiver decision alongside current retained delivery status. An unavailable current row does not
    rewrite or invalidate the original result.

    """

    target: EventRecoveryNotificationRetryTarget
    """Exact receiver selection and expected delivery generation copied from a recovery notification retry preview.
    Repeated delivery IDs are rejected."""
    state: EventRecoveryNotificationRetryDecisionState
    current_delivery_status: str
    retry_outcome: EventRecoveryNotificationRetryDecisionRetryOutcome
    """Outcome of the originally queued generation. Retained terminal attempts prove success or failure; matching
    active delivery proves pending. Missing evidence is unknown and skipped decisions are not applicable."""
    retained_attempt_count: int
    """Number of retained completed attempts for the originally queued generation, excluding other generations and
    in-flight attempts."""
    attempt_count_complete: bool
    """True when retained attempt numbers form a complete sequence through the known outcome; unknown outcomes
    always report false. Not applicable decisions have a complete zero count."""
    reason: str | Unset = UNSET
    replay_generation: int | Unset = UNSET
    current_replay_generation: int | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    """Finish time of the retained terminal attempt for the originally queued generation, present only for
    succeeded or failed outcomes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target.to_dict()

        state: str = self.state

        current_delivery_status = self.current_delivery_status

        retry_outcome: str = self.retry_outcome

        retained_attempt_count = self.retained_attempt_count

        attempt_count_complete = self.attempt_count_complete

        reason = self.reason

        replay_generation = self.replay_generation

        current_replay_generation = self.current_replay_generation

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "state": state,
                "current_delivery_status": current_delivery_status,
                "retry_outcome": retry_outcome,
                "retained_attempt_count": retained_attempt_count,
                "attempt_count_complete": attempt_count_complete,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if replay_generation is not UNSET:
            field_dict["replay_generation"] = replay_generation
        if current_replay_generation is not UNSET:
            field_dict["current_replay_generation"] = current_replay_generation
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget

        d = dict(src_dict)
        target = EventRecoveryNotificationRetryTarget.from_dict(d.pop("target"))

        state = check_event_recovery_notification_retry_decision_state(d.pop("state"))

        current_delivery_status = d.pop("current_delivery_status")

        retry_outcome = check_event_recovery_notification_retry_decision_retry_outcome(d.pop("retry_outcome"))

        retained_attempt_count = d.pop("retained_attempt_count")

        attempt_count_complete = d.pop("attempt_count_complete")

        reason = d.pop("reason", UNSET)

        replay_generation = d.pop("replay_generation", UNSET)

        current_replay_generation = d.pop("current_replay_generation", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        event_recovery_notification_retry_decision = cls(
            target=target,
            state=state,
            current_delivery_status=current_delivery_status,
            retry_outcome=retry_outcome,
            retained_attempt_count=retained_attempt_count,
            attempt_count_complete=attempt_count_complete,
            reason=reason,
            replay_generation=replay_generation,
            current_replay_generation=current_replay_generation,
            completed_at=completed_at,
        )

        event_recovery_notification_retry_decision.additional_properties = d
        return event_recovery_notification_retry_decision

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
