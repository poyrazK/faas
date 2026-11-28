from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.trigger_consumer_status_liveness import (
    TriggerConsumerStatusLiveness,
    check_trigger_consumer_status_liveness,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="TriggerConsumerStatus")


@_attrs_define
class TriggerConsumerStatus:
    """The scheduler's last observation of a trigger's broker consumer.
    Absent until the trigger's first poll.

    """

    liveness: TriggerConsumerStatusLiveness
    """`degraded`: the last poll failed (see last_error). `stale`: no
    poll for 30 s (the trigger is disabled, its app cannot run, or
    no scheduler polls it).
    """
    last_poll_at: datetime.datetime | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    last_error_at: datetime.datetime | Unset = UNSET
    last_error: str | Unset = UNSET
    lag_messages: int | Unset = UNSET
    """The broker's backlog for the consumer. Kafka: high watermark
    less the group's committed offset, summed over partitions.
    NATS JetStream: the consumer's pending messages. Redis Streams:
    the group's lag.
    """
    lag_age_seconds: float | Unset = UNSET
    """Age of the oldest record the last poll returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        liveness: str = self.liveness

        last_poll_at: str | Unset = UNSET
        if not isinstance(self.last_poll_at, Unset):
            last_poll_at = self.last_poll_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        last_error_at: str | Unset = UNSET
        if not isinstance(self.last_error_at, Unset):
            last_error_at = self.last_error_at.isoformat()

        last_error = self.last_error

        lag_messages = self.lag_messages

        lag_age_seconds = self.lag_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "liveness": liveness,
            }
        )
        if last_poll_at is not UNSET:
            field_dict["last_poll_at"] = last_poll_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if last_error_at is not UNSET:
            field_dict["last_error_at"] = last_error_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if lag_messages is not UNSET:
            field_dict["lag_messages"] = lag_messages
        if lag_age_seconds is not UNSET:
            field_dict["lag_age_seconds"] = lag_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        liveness = check_trigger_consumer_status_liveness(d.pop("liveness"))

        _last_poll_at = d.pop("last_poll_at", UNSET)
        last_poll_at: datetime.datetime | Unset
        if isinstance(_last_poll_at, Unset):
            last_poll_at = UNSET
        else:
            last_poll_at = datetime.datetime.fromisoformat(_last_poll_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        _last_error_at = d.pop("last_error_at", UNSET)
        last_error_at: datetime.datetime | Unset
        if isinstance(_last_error_at, Unset):
            last_error_at = UNSET
        else:
            last_error_at = datetime.datetime.fromisoformat(_last_error_at)

        last_error = d.pop("last_error", UNSET)

        lag_messages = d.pop("lag_messages", UNSET)

        lag_age_seconds = d.pop("lag_age_seconds", UNSET)

        trigger_consumer_status = cls(
            liveness=liveness,
            last_poll_at=last_poll_at,
            last_success_at=last_success_at,
            last_error_at=last_error_at,
            last_error=last_error,
            lag_messages=lag_messages,
            lag_age_seconds=lag_age_seconds,
        )

        trigger_consumer_status.additional_properties = d
        return trigger_consumer_status

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
