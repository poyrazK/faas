from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.feature_flag import FeatureFlag
    from ..models.flags_config_groups import FlagsConfigGroups


T = TypeVar("T", bound="FlagsConfig")


@_attrs_define
class FlagsConfig:
    """Complete atomic environment configuration; omitted flags are removed from current evaluation."""

    flags: list[FeatureFlag]
    groups: FlagsConfigGroups

    def to_dict(self) -> dict[str, Any]:
        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        groups = self.groups.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "flags": flags,
                "groups": groups,
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

        flags_config = cls(
            flags=flags,
            groups=groups,
        )

        return flags_config
