from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.operator_platform_flag_groups_groups import OperatorPlatformFlagGroupsGroups
    from ..models.operator_platform_flag_groups_references import OperatorPlatformFlagGroupsReferences


T = TypeVar("T", bound="OperatorPlatformFlagGroups")


@_attrs_define
class OperatorPlatformFlagGroups:
    """Shared named account sets referenced by platform-flag rules."""

    config_key: str
    groups: OperatorPlatformFlagGroupsGroups
    references: OperatorPlatformFlagGroupsReferences
    version: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        config_key = self.config_key

        groups = self.groups.to_dict()

        references = self.references.to_dict()

        version = self.version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "config_key": config_key,
                "groups": groups,
                "references": references,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operator_platform_flag_groups_groups import OperatorPlatformFlagGroupsGroups
        from ..models.operator_platform_flag_groups_references import OperatorPlatformFlagGroupsReferences

        d = dict(src_dict)
        config_key = d.pop("config_key")

        groups = OperatorPlatformFlagGroupsGroups.from_dict(d.pop("groups"))

        references = OperatorPlatformFlagGroupsReferences.from_dict(d.pop("references"))

        version = d.pop("version")

        operator_platform_flag_groups = cls(
            config_key=config_key,
            groups=groups,
            references=references,
            version=version,
        )

        operator_platform_flag_groups.additional_properties = d
        return operator_platform_flag_groups

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
