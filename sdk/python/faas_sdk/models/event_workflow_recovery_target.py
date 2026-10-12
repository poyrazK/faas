from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventWorkflowRecoveryTarget")


@_attrs_define
class EventWorkflowRecoveryTarget:
    """One captured workflow recipient; event IDs must be distinct. Generation is required for independent recipient
    routing and omitted for legacy receipts. Replay count also fences legacy recovery cycles. No delivery-age override
    is supported.

    """

    event_id: str
    subscription_id: str
    replay_count: int
    generation: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        subscription_id = self.subscription_id

        replay_count = self.replay_count

        generation = self.generation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "subscription_id": subscription_id,
                "replay_count": replay_count,
            }
        )
        if generation is not UNSET:
            field_dict["generation"] = generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = d.pop("event_id")

        subscription_id = d.pop("subscription_id")

        replay_count = d.pop("replay_count")

        generation = d.pop("generation", UNSET)

        event_workflow_recovery_target = cls(
            event_id=event_id,
            subscription_id=subscription_id,
            replay_count=replay_count,
            generation=generation,
        )

        return event_workflow_recovery_target
