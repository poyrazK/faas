from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupClaimRequest")


@_attrs_define
class RealtimeConsumerGroupClaimRequest:
    """Realtime Consumer Group Claim Request."""

    worker: str
    wait_seconds: int | Unset = 0
    """Realtime consumer group claim request properties wait seconds. Wait for available work, checking every 500
    ms without holding a transaction. Returns promptly on pause/drain or blockers. At most 64 waiting requests per
    apid. Cancellation stops waiting; uncertain claims must not be retried automatically."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        worker = self.worker

        wait_seconds = self.wait_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "worker": worker,
            }
        )
        if wait_seconds is not UNSET:
            field_dict["wait_seconds"] = wait_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        worker = d.pop("worker")

        wait_seconds = d.pop("wait_seconds", UNSET)

        realtime_consumer_group_claim_request = cls(
            worker=worker,
            wait_seconds=wait_seconds,
        )

        realtime_consumer_group_claim_request.additional_properties = d
        return realtime_consumer_group_claim_request

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
