from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeConsumerGroupResyncRequest")


@_attrs_define
class RealtimeConsumerGroupResyncRequest:
    """Realtime Consumer Group Resync Request."""

    expected_sequence: str
    after_sequence: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_sequence = self.expected_sequence

        after_sequence = self.after_sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_sequence": expected_sequence,
                "after_sequence": after_sequence,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_sequence = d.pop("expected_sequence")

        after_sequence = d.pop("after_sequence")

        realtime_consumer_group_resync_request = cls(
            expected_sequence=expected_sequence,
            after_sequence=after_sequence,
        )

        realtime_consumer_group_resync_request.additional_properties = d
        return realtime_consumer_group_resync_request

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
