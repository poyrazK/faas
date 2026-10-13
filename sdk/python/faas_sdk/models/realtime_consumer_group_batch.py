from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.realtime_consumer_group_claim import RealtimeConsumerGroupClaim


T = TypeVar("T", bound="RealtimeConsumerGroupBatch")


@_attrs_define
class RealtimeConsumerGroupBatch:
    """Realtime Consumer Group Batch."""

    deduplication_blocked: bool
    """A new event ID encountered the 4096-key bound; its source cursor is unchanged. Partial claims may be
    present. Existing snapshots bypass deduplication and remain claimable. Wait using retry_after_milliseconds
    before assigning new IDs."""
    circuit_open: bool
    """Circuit cooldown or an active probe stopped new claims; partial claims may be present."""
    claims: list[RealtimeConsumerGroupClaim]
    history_unavailable: bool
    capacity_blocked: bool
    paused: bool
    """this realtime operation. New claims are paused; existing leases remain valid."""
    ordering_blocked: bool
    """Ready snapshots are blocked by same-key predecessors or leases. This is normal backpressure, not a fatal
    error."""
    rate_throttled: bool
    """Shared group rate stopped admission, possibly after partial claims."""
    retry_after_milliseconds: int
    """Minimum observed rate or circuit delay or time until an event ID expires. Positive when rate_throttled,
    circuit_open or deduplication_blocked."""
    filter_scan_limited: bool
    """Assignment reached the 128-event scan budget. Progress is committed; partial claims may be present. Poll
    again to continue; more work is not guaranteed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deduplication_blocked = self.deduplication_blocked

        circuit_open = self.circuit_open

        claims = []
        for claims_item_data in self.claims:
            claims_item = claims_item_data.to_dict()
            claims.append(claims_item)

        history_unavailable = self.history_unavailable

        capacity_blocked = self.capacity_blocked

        paused = self.paused

        ordering_blocked = self.ordering_blocked

        rate_throttled = self.rate_throttled

        retry_after_milliseconds = self.retry_after_milliseconds

        filter_scan_limited = self.filter_scan_limited

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deduplication_blocked": deduplication_blocked,
                "circuit_open": circuit_open,
                "claims": claims,
                "history_unavailable": history_unavailable,
                "capacity_blocked": capacity_blocked,
                "paused": paused,
                "ordering_blocked": ordering_blocked,
                "rate_throttled": rate_throttled,
                "retry_after_milliseconds": retry_after_milliseconds,
                "filter_scan_limited": filter_scan_limited,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.realtime_consumer_group_claim import RealtimeConsumerGroupClaim

        d = dict(src_dict)
        deduplication_blocked = d.pop("deduplication_blocked")

        circuit_open = d.pop("circuit_open")

        claims = []
        _claims = d.pop("claims")
        for claims_item_data in _claims:
            claims_item = RealtimeConsumerGroupClaim.from_dict(claims_item_data)

            claims.append(claims_item)

        history_unavailable = d.pop("history_unavailable")

        capacity_blocked = d.pop("capacity_blocked")

        paused = d.pop("paused")

        ordering_blocked = d.pop("ordering_blocked")

        rate_throttled = d.pop("rate_throttled")

        retry_after_milliseconds = d.pop("retry_after_milliseconds")

        filter_scan_limited = d.pop("filter_scan_limited")

        realtime_consumer_group_batch = cls(
            deduplication_blocked=deduplication_blocked,
            circuit_open=circuit_open,
            claims=claims,
            history_unavailable=history_unavailable,
            capacity_blocked=capacity_blocked,
            paused=paused,
            ordering_blocked=ordering_blocked,
            rate_throttled=rate_throttled,
            retry_after_milliseconds=retry_after_milliseconds,
            filter_scan_limited=filter_scan_limited,
        )

        realtime_consumer_group_batch.additional_properties = d
        return realtime_consumer_group_batch

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
