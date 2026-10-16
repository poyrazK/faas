from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_priority_status_fair_turns import (
    RealtimeConsumerGroupPriorityStatusFairTurns,
    check_realtime_consumer_group_priority_status_fair_turns,
)

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_priority_backlog import RealtimeConsumerGroupPriorityBacklog


T = TypeVar("T", bound="RealtimeConsumerGroupPriorityStatus")


@_attrs_define
class RealtimeConsumerGroupPriorityStatus:
    """Realtime Consumer Group Priority Status."""

    metadata_key: str
    fair_turns: RealtimeConsumerGroupPriorityStatusFairTurns
    next_claim_oldest: bool
    backlog: list[RealtimeConsumerGroupPriorityBacklog]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        metadata_key = self.metadata_key

        fair_turns: int = self.fair_turns

        next_claim_oldest = self.next_claim_oldest

        backlog = []
        for backlog_item_data in self.backlog:
            backlog_item = backlog_item_data.to_dict()
            backlog.append(backlog_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "metadata_key": metadata_key,
                "fair_turns": fair_turns,
                "next_claim_oldest": next_claim_oldest,
                "backlog": backlog,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_priority_backlog import RealtimeConsumerGroupPriorityBacklog

        d = dict(src_dict)
        metadata_key = d.pop("metadata_key")

        fair_turns = check_realtime_consumer_group_priority_status_fair_turns(d.pop("fair_turns"))

        next_claim_oldest = d.pop("next_claim_oldest")

        backlog = []
        _backlog = d.pop("backlog")
        for backlog_item_data in _backlog:
            backlog_item = RealtimeConsumerGroupPriorityBacklog.from_dict(backlog_item_data)

            backlog.append(backlog_item)

        realtime_consumer_group_priority_status = cls(
            metadata_key=metadata_key,
            fair_turns=fair_turns,
            next_claim_oldest=next_claim_oldest,
            backlog=backlog,
        )

        realtime_consumer_group_priority_status.additional_properties = d
        return realtime_consumer_group_priority_status

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
