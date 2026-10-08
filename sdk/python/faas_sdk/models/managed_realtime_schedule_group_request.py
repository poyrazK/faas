from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_realtime_schedule_group_request_expected_versions import (
        ManagedRealtimeScheduleGroupRequestExpectedVersions,
    )


T = TypeVar("T", bound="ManagedRealtimeScheduleGroupRequest")


@_attrs_define
class ManagedRealtimeScheduleGroupRequest:
    expected_versions: ManagedRealtimeScheduleGroupRequestExpectedVersions
    """Exact schedule ID/version map of all pending or paused group members; terminal members excluded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expected_versions = self.expected_versions.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expected_versions": expected_versions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_schedule_group_request_expected_versions import (
            ManagedRealtimeScheduleGroupRequestExpectedVersions,
        )

        d = dict(src_dict)
        expected_versions = ManagedRealtimeScheduleGroupRequestExpectedVersions.from_dict(d.pop("expected_versions"))

        managed_realtime_schedule_group_request = cls(
            expected_versions=expected_versions,
        )

        managed_realtime_schedule_group_request.additional_properties = d
        return managed_realtime_schedule_group_request

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
