from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_settlement import RealtimeConsumerGroupSettlement


T = TypeVar("T", bound="RealtimeConsumerGroupBatchSettlementRequest")


@_attrs_define
class RealtimeConsumerGroupBatchSettlementRequest:
    """Realtime Consumer Group Batch Settlement Request."""

    settlements: list[RealtimeConsumerGroupSettlement]
    idempotency_key: UUID | Unset = UNSET
    """Optional UUID. Retains the original successful result for 24 hours for this group and principal. Reuse
    requires identical normalized contents, including claim tokens and ordered batch items. At most 1024 receipts
    per group; full capacity returns 429 before settlement. Omit item-level keys in batches. Replays require current
    authorization and do not settle again. Renew receipts report the original lease expiry rather than extending it
    again."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        settlements = []
        for settlements_item_data in self.settlements:
            settlements_item = settlements_item_data.to_dict()
            settlements.append(settlements_item)

        idempotency_key: str | Unset = UNSET
        if not isinstance(self.idempotency_key, Unset):
            idempotency_key = str(self.idempotency_key)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "settlements": settlements,
            }
        )
        if idempotency_key is not UNSET:
            field_dict["idempotency_key"] = idempotency_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_settlement import RealtimeConsumerGroupSettlement

        d = dict(src_dict)
        settlements = []
        _settlements = d.pop("settlements")
        for settlements_item_data in _settlements:
            settlements_item = RealtimeConsumerGroupSettlement.from_dict(settlements_item_data)

            settlements.append(settlements_item)

        _idempotency_key = d.pop("idempotency_key", UNSET)
        idempotency_key: UUID | Unset
        if isinstance(_idempotency_key, Unset):
            idempotency_key = UNSET
        else:
            idempotency_key = UUID(_idempotency_key)

        realtime_consumer_group_batch_settlement_request = cls(
            settlements=settlements,
            idempotency_key=idempotency_key,
        )

        realtime_consumer_group_batch_settlement_request.additional_properties = d
        return realtime_consumer_group_batch_settlement_request

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
