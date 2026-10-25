from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupRetentionRequest")


@_attrs_define
class RealtimeConsumerGroupRetentionRequest:
    """Realtime Consumer Group Retention Request."""

    retention_seconds: int
    """Zero disables cleanup; otherwise 3600 to 2592000. Ordered groups require skip."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        retention_seconds = self.retention_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "retention_seconds": retention_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retention_seconds = d.pop("retention_seconds")

        realtime_consumer_group_retention_request = cls(
            retention_seconds=retention_seconds,
        )

        realtime_consumer_group_retention_request.additional_properties = d
        return realtime_consumer_group_retention_request

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
