from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupRetryPolicyRequest")


@_attrs_define
class RealtimeConsumerGroupRetryPolicyRequest:
    """Realtime Consumer Group Retry Policy Request."""

    enabled: bool
    base_delay_seconds: int
    max_delay_seconds: int
    """Must be at least base_delay_seconds."""
    jitter: bool
    permanent_failure_codes: list[str] | Unset = UNSET
    """Exact case-sensitive codes; unspecified codes remain retryable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        base_delay_seconds = self.base_delay_seconds

        max_delay_seconds = self.max_delay_seconds

        jitter = self.jitter

        permanent_failure_codes: list[str] | Unset = UNSET
        if not isinstance(self.permanent_failure_codes, Unset):
            permanent_failure_codes = self.permanent_failure_codes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "base_delay_seconds": base_delay_seconds,
                "max_delay_seconds": max_delay_seconds,
                "jitter": jitter,
            }
        )
        if permanent_failure_codes is not UNSET:
            field_dict["permanent_failure_codes"] = permanent_failure_codes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        base_delay_seconds = d.pop("base_delay_seconds")

        max_delay_seconds = d.pop("max_delay_seconds")

        jitter = d.pop("jitter")

        permanent_failure_codes = cast(list[str], d.pop("permanent_failure_codes", UNSET))

        realtime_consumer_group_retry_policy_request = cls(
            enabled=enabled,
            base_delay_seconds=base_delay_seconds,
            max_delay_seconds=max_delay_seconds,
            jitter=jitter,
            permanent_failure_codes=permanent_failure_codes,
        )

        realtime_consumer_group_retry_policy_request.additional_properties = d
        return realtime_consumer_group_retry_policy_request

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
