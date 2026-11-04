from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.feature_flag import FeatureFlag
    from ..models.flags_config_groups import FlagsConfigGroups


T = TypeVar("T", bound="FlagsBundle")


@_attrs_define
class FlagsBundle:
    """Immutable runtime configuration snapshot."""

    flags: list[FeatureFlag]
    groups: FlagsConfigGroups
    environment_id: UUID
    version: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        groups = self.groups.to_dict()

        environment_id = str(self.environment_id)

        version = self.version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flags": flags,
                "groups": groups,
                "environment_id": environment_id,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.feature_flag import FeatureFlag
        from ..models.flags_config_groups import FlagsConfigGroups

        d = dict(src_dict)
        flags = []
        _flags = d.pop("flags")
        for flags_item_data in _flags:
            flags_item = FeatureFlag.from_dict(flags_item_data)

            flags.append(flags_item)

        groups = FlagsConfigGroups.from_dict(d.pop("groups"))

        environment_id = UUID(d.pop("environment_id"))

        version = d.pop("version")

        flags_bundle = cls(
            flags=flags,
            groups=groups,
            environment_id=environment_id,
            version=version,
        )

        flags_bundle.additional_properties = d
        return flags_bundle

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
