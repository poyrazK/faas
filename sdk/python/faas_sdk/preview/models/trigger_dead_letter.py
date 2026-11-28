from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.trigger_dead_letter_reason import TriggerDeadLetterReason, check_trigger_dead_letter_reason
from ..models.trigger_routed_to import TriggerRoutedTo, check_trigger_routed_to

if TYPE_CHECKING:
    from ..models.trigger_dead_letter_detail import TriggerDeadLetterDetail


T = TypeVar("T", bound="TriggerDeadLetter")


@_attrs_define
class TriggerDeadLetter:
    """Read-only wire shape for one trigger_dead_letter row."""

    record_id: str
    trigger_id: str
    reason: TriggerDeadLetterReason
    """Reason enum pinned by SQL CHECK on trigger_dead_letter.reason."""
    routed_to: TriggerRoutedTo
    """Where a dead-lettered record was routed."""
    detail: TriggerDeadLetterDetail
    """Opaque per-reason JSON (broker-error vs poison-record shapes differ)."""
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        record_id = self.record_id

        trigger_id = self.trigger_id

        reason: str = self.reason

        routed_to: str = self.routed_to

        detail = self.detail.to_dict()

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "record_id": record_id,
                "trigger_id": trigger_id,
                "reason": reason,
                "routed_to": routed_to,
                "detail": detail,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.trigger_dead_letter_detail import TriggerDeadLetterDetail

        d = dict(src_dict)
        record_id = d.pop("record_id")

        trigger_id = d.pop("trigger_id")

        reason = check_trigger_dead_letter_reason(d.pop("reason"))

        routed_to = check_trigger_routed_to(d.pop("routed_to"))

        detail = TriggerDeadLetterDetail.from_dict(d.pop("detail"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        trigger_dead_letter = cls(
            record_id=record_id,
            trigger_id=trigger_id,
            reason=reason,
            routed_to=routed_to,
            detail=detail,
            created_at=created_at,
        )

        trigger_dead_letter.additional_properties = d
        return trigger_dead_letter

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
