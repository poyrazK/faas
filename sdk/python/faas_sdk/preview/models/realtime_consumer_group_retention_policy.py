from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_retention_policy_warning_seconds import (
    RealtimeConsumerGroupRetentionPolicyWarningSeconds,
    check_realtime_consumer_group_retention_policy_warning_seconds,
)

T = TypeVar("T", bound="RealtimeConsumerGroupRetentionPolicy")


@_attrs_define
class RealtimeConsumerGroupRetentionPolicy:
    """Realtime Consumer Group Retention Policy."""

    retention_seconds: int
    warning_seconds: RealtimeConsumerGroupRetentionPolicyWarningSeconds
    expired_dead_letters: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        retention_seconds = self.retention_seconds

        warning_seconds: int = self.warning_seconds

        expired_dead_letters = self.expired_dead_letters

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "retention_seconds": retention_seconds,
                "warning_seconds": warning_seconds,
                "expired_dead_letters": expired_dead_letters,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retention_seconds = d.pop("retention_seconds")

        warning_seconds = check_realtime_consumer_group_retention_policy_warning_seconds(d.pop("warning_seconds"))

        expired_dead_letters = d.pop("expired_dead_letters")

        realtime_consumer_group_retention_policy = cls(
            retention_seconds=retention_seconds,
            warning_seconds=warning_seconds,
            expired_dead_letters=expired_dead_letters,
        )

        realtime_consumer_group_retention_policy.additional_properties = d
        return realtime_consumer_group_retention_policy

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
