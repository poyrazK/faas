from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EventConsumerWaitingReasonsCounts")


@_attrs_define
class EventConsumerWaitingReasonsCounts:
    """Counts for circuit_open, circuit_probe_wait, circuit_recovery_rate_limited, subscription_paused,
    subscription_rate_limited, ordering_blocked, capacity_consumer, capacity_app, capacity_account, routing_in_progress,
    receipt_processing, retry_backoff, workflow_routing, and ready. Known reasons include zero counts; processing
    recipients are excluded.

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_consumer_waiting_reasons_counts = cls()

        event_consumer_waiting_reasons_counts.additional_properties = d
        return event_consumer_waiting_reasons_counts

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> int:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: int) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
