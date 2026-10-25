from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsRecordingAlertReference")


@_attrs_define
class ObsRecordingAlertReference:
    """Safe expected alert identity from the selected catalog; independent of historical episode keys."""

    id: str
    file_name: str
    group_name: str
    rule_name: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        file_name = self.file_name

        group_name = self.group_name

        rule_name = self.rule_name

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "file_name": file_name,
                "group_name": group_name,
                "rule_name": rule_name,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        file_name = d.pop("file_name")

        group_name = d.pop("group_name")

        rule_name = d.pop("rule_name")

        obs_recording_alert_reference = cls(
            id=id,
            file_name=file_name,
            group_name=group_name,
            rule_name=rule_name,
        )

        obs_recording_alert_reference.additional_properties = d
        return obs_recording_alert_reference

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
