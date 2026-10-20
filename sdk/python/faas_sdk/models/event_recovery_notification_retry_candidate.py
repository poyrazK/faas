from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_recovery_notification_retry_candidate_kind import (
    EventRecoveryNotificationRetryCandidateKind,
    check_event_recovery_notification_retry_candidate_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryNotificationRetryCandidate")


@_attrs_define
class EventRecoveryNotificationRetryCandidate:
    """One observed receiver with its current generation and eligibility. Missing delivery identity prevents selecting this
    receiver for retry.

    """

    kind: EventRecoveryNotificationRetryCandidateKind
    webhook_id: UUID
    replay_generation: int
    status: str
    eligible: bool
    delivery_id: UUID | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        webhook_id = str(self.webhook_id)

        replay_generation = self.replay_generation

        status = self.status

        eligible = self.eligible

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "webhook_id": webhook_id,
                "replay_generation": replay_generation,
                "status": status,
                "eligible": eligible,
            }
        )
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_event_recovery_notification_retry_candidate_kind(d.pop("kind"))

        webhook_id = UUID(d.pop("webhook_id"))

        replay_generation = d.pop("replay_generation")

        status = d.pop("status")

        eligible = d.pop("eligible")

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        reason = d.pop("reason", UNSET)

        event_recovery_notification_retry_candidate = cls(
            kind=kind,
            webhook_id=webhook_id,
            replay_generation=replay_generation,
            status=status,
            eligible=eligible,
            delivery_id=delivery_id,
            reason=reason,
        )

        event_recovery_notification_retry_candidate.additional_properties = d
        return event_recovery_notification_retry_candidate

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
