from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.durable_entity_head_delivery import DurableEntityHeadDelivery


T = TypeVar("T", bound="DurableEntityOutboxInspection")


@_attrs_define
class DurableEntityOutboxInspection:
    pending: int
    attempts: int
    exhausted: bool
    head_id: UUID | Unset = UNSET
    next_attempt_at: datetime.datetime | Unset = UNSET
    head_delivery: DurableEntityHeadDelivery | Unset = UNSET
    """Separate observation for the pending head only; absent when no head is pending. Unknown includes
    unavailable/pruned history, not proof of non-acceptance."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        pending = self.pending

        attempts = self.attempts

        exhausted = self.exhausted

        head_id: str | Unset = UNSET
        if not isinstance(self.head_id, Unset):
            head_id = str(self.head_id)

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        head_delivery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.head_delivery, Unset):
            head_delivery = self.head_delivery.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "pending": pending,
                "attempts": attempts,
                "exhausted": exhausted,
            }
        )
        if head_id is not UNSET:
            field_dict["head_id"] = head_id
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if head_delivery is not UNSET:
            field_dict["head_delivery"] = head_delivery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.durable_entity_head_delivery import DurableEntityHeadDelivery

        d = dict(src_dict)
        pending = d.pop("pending")

        attempts = d.pop("attempts")

        exhausted = d.pop("exhausted")

        _head_id = d.pop("head_id", UNSET)
        head_id: UUID | Unset
        if isinstance(_head_id, Unset):
            head_id = UNSET
        else:
            head_id = UUID(_head_id)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _head_delivery = d.pop("head_delivery", UNSET)
        head_delivery: DurableEntityHeadDelivery | Unset
        if isinstance(_head_delivery, Unset):
            head_delivery = UNSET
        else:
            head_delivery = DurableEntityHeadDelivery.from_dict(_head_delivery)

        durable_entity_outbox_inspection = cls(
            pending=pending,
            attempts=attempts,
            exhausted=exhausted,
            head_id=head_id,
            next_attempt_at=next_attempt_at,
            head_delivery=head_delivery,
        )

        durable_entity_outbox_inspection.additional_properties = d
        return durable_entity_outbox_inspection

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
