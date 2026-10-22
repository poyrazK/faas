from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupClaimRateStatus")


@_attrs_define
class RealtimeConsumerGroupClaimRateStatus:
    """Realtime Consumer Group Claim Rate Status."""

    rate_per_second: int
    burst: int
    throttled_claim_requests: str
    """Cumulative committed batch admission decisions stopped by rate, including partial batches. Counts polling
    attempts, not withheld messages."""
    retry_after_milliseconds: int
    """Current earliest rate allowance delay; zero when available or disabled."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        burst = self.burst

        throttled_claim_requests = self.throttled_claim_requests

        retry_after_milliseconds = self.retry_after_milliseconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rate_per_second": rate_per_second,
                "burst": burst,
                "throttled_claim_requests": throttled_claim_requests,
                "retry_after_milliseconds": retry_after_milliseconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second")

        burst = d.pop("burst")

        throttled_claim_requests = d.pop("throttled_claim_requests")

        retry_after_milliseconds = d.pop("retry_after_milliseconds")

        realtime_consumer_group_claim_rate_status = cls(
            rate_per_second=rate_per_second,
            burst=burst,
            throttled_claim_requests=throttled_claim_requests,
            retry_after_milliseconds=retry_after_milliseconds,
        )

        realtime_consumer_group_claim_rate_status.additional_properties = d
        return realtime_consumer_group_claim_rate_status

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
