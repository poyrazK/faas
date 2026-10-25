from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_retry_result_state import (
    EventRecoveryNotificationRetryResultState,
    check_event_recovery_notification_retry_result_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget


T = TypeVar("T", bound="EventRecoveryNotificationRetryResult")


@_attrs_define
class EventRecoveryNotificationRetryResult:
    """Frozen decision for an explicitly requested receiver. Queued starts a new delivery generation; skipped preserves
    delivery state and explains the reason.

    """

    target: EventRecoveryNotificationRetryTarget
    """Exact receiver selection and expected delivery generation copied from a recovery notification retry preview.
    Repeated delivery IDs are rejected."""
    state: EventRecoveryNotificationRetryResultState
    reason: str | Unset = UNSET
    replay_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        target = self.target.to_dict()

        state: str = self.state

        reason = self.reason

        replay_generation = self.replay_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "target": target,
                "state": state,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if replay_generation is not UNSET:
            field_dict["replay_generation"] = replay_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_notification_retry_target import EventRecoveryNotificationRetryTarget

        d = dict(src_dict)
        target = EventRecoveryNotificationRetryTarget.from_dict(d.pop("target"))

        state = check_event_recovery_notification_retry_result_state(d.pop("state"))

        reason = d.pop("reason", UNSET)

        replay_generation = d.pop("replay_generation", UNSET)

        event_recovery_notification_retry_result = cls(
            target=target,
            state=state,
            reason=reason,
            replay_generation=replay_generation,
        )

        event_recovery_notification_retry_result.additional_properties = d
        return event_recovery_notification_retry_result

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
