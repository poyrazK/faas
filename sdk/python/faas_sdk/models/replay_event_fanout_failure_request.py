from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ReplayEventFanoutFailureRequest")


@_attrs_define
class ReplayEventFanoutFailureRequest:
    """Identity of one terminal fanout failure to replay."""

    event_id: str
    event_source: str
    subscription_id: UUID
    expected_accepted_at: str | Unset = UNSET
    """Optional exact original acceptance instant. Checked under the receipt row lock in the replay transaction;
    replacement returns 409 without mutation. Guarded replay targets the original acceptance snapshot, not
    backfills."""
    allow_expired: bool | Unset = False
    """Allow expired in the replay event fanout failure request: explicitly override delivery age for this replay
    generation or historical backfill job. Preserves deterministic invocation identity and manual controls."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        subscription_id = str(self.subscription_id)

        expected_accepted_at: str | Unset = UNSET
        if not isinstance(self.expected_accepted_at, Unset):
            expected_accepted_at = self.expected_accepted_at

        allow_expired = self.allow_expired

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "subscription_id": subscription_id,
            }
        )
        if expected_accepted_at is not UNSET:
            field_dict["expected_accepted_at"] = expected_accepted_at
        if allow_expired is not UNSET:
            field_dict["allow_expired"] = allow_expired

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        subscription_id = UUID(d.pop("subscription_id"))

        _expected_accepted_at = d.pop("expected_accepted_at", UNSET)
        expected_accepted_at: str | Unset
        if isinstance(_expected_accepted_at, Unset):
            expected_accepted_at = UNSET
        else:
            expected_accepted_at = _expected_accepted_at

        allow_expired = d.pop("allow_expired", UNSET)

        replay_event_fanout_failure_request = cls(
            event_id=event_id,
            event_source=event_source,
            subscription_id=subscription_id,
            expected_accepted_at=expected_accepted_at,
            allow_expired=allow_expired,
        )

        return replay_event_fanout_failure_request
