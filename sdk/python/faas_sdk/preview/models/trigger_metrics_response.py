from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.trigger_consumer_status import TriggerConsumerStatus


T = TypeVar("T", bound="TriggerMetricsResponse")


@_attrs_define
class TriggerMetricsResponse:
    """Aggregated counters per state for one trigger, and the scheduler's
    last observation of its broker consumer. NOT a Prometheus
    scrape — /v1/metrics is the Prometheus surface.

    """

    trigger_id: str
    pending_count: int
    claimed_count: int
    succeeded_count: int
    retry_count: int
    dead_letter_count: int
    count_limit: int | Unset = UNSET
    """Where each count stops; a count equal to it means at least that many."""
    consumer: TriggerConsumerStatus | Unset = UNSET
    """The scheduler's last observation of a trigger's broker consumer.
    Absent until the trigger's first poll.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trigger_id = self.trigger_id

        pending_count = self.pending_count

        claimed_count = self.claimed_count

        succeeded_count = self.succeeded_count

        retry_count = self.retry_count

        dead_letter_count = self.dead_letter_count

        count_limit = self.count_limit

        consumer: dict[str, Any] | Unset = UNSET
        if not isinstance(self.consumer, Unset):
            consumer = self.consumer.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trigger_id": trigger_id,
                "pending_count": pending_count,
                "claimed_count": claimed_count,
                "succeeded_count": succeeded_count,
                "retry_count": retry_count,
                "dead_letter_count": dead_letter_count,
            }
        )
        if count_limit is not UNSET:
            field_dict["count_limit"] = count_limit
        if consumer is not UNSET:
            field_dict["consumer"] = consumer

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.trigger_consumer_status import TriggerConsumerStatus

        d = dict(src_dict)
        trigger_id = d.pop("trigger_id")

        pending_count = d.pop("pending_count")

        claimed_count = d.pop("claimed_count")

        succeeded_count = d.pop("succeeded_count")

        retry_count = d.pop("retry_count")

        dead_letter_count = d.pop("dead_letter_count")

        count_limit = d.pop("count_limit", UNSET)

        _consumer = d.pop("consumer", UNSET)
        consumer: TriggerConsumerStatus | Unset
        if isinstance(_consumer, Unset):
            consumer = UNSET
        else:
            consumer = TriggerConsumerStatus.from_dict(_consumer)

        trigger_metrics_response = cls(
            trigger_id=trigger_id,
            pending_count=pending_count,
            claimed_count=claimed_count,
            succeeded_count=succeeded_count,
            retry_count=retry_count,
            dead_letter_count=dead_letter_count,
            count_limit=count_limit,
            consumer=consumer,
        )

        trigger_metrics_response.additional_properties = d
        return trigger_metrics_response

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
