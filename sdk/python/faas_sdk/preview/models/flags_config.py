from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.feature_flag import FeatureFlag
    from ..models.flag_holdout import FlagHoldout
    from ..models.flag_layer import FlagLayer
    from ..models.flags_config_groups import FlagsConfigGroups


T = TypeVar("T", bound="FlagsConfig")


@_attrs_define
class FlagsConfig:
    """Complete atomic environment configuration; omitted flags are removed from current evaluation."""

    flags: list[FeatureFlag]
    groups: FlagsConfigGroups
    holdout: FlagHoldout | Unset = UNSET
    """The environment holdout: basis_points of customers, chosen by seed, see every running experiment's control."""
    layers: list[FlagLayer] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        groups = self.groups.to_dict()

        holdout: dict[str, Any] | Unset = UNSET
        if not isinstance(self.holdout, Unset):
            holdout = self.holdout.to_dict()

        layers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.layers, Unset):
            layers = []
            for layers_item_data in self.layers:
                layers_item = layers_item_data.to_dict()
                layers.append(layers_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "flags": flags,
                "groups": groups,
            }
        )
        if holdout is not UNSET:
            field_dict["holdout"] = holdout
        if layers is not UNSET:
            field_dict["layers"] = layers

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.feature_flag import FeatureFlag
        from ..models.flag_holdout import FlagHoldout
        from ..models.flag_layer import FlagLayer
        from ..models.flags_config_groups import FlagsConfigGroups

        d = dict(src_dict)
        flags = []
        _flags = d.pop("flags")
        for flags_item_data in _flags:
            flags_item = FeatureFlag.from_dict(flags_item_data)

            flags.append(flags_item)

        groups = FlagsConfigGroups.from_dict(d.pop("groups"))

        _holdout = d.pop("holdout", UNSET)
        holdout: FlagHoldout | Unset
        if isinstance(_holdout, Unset):
            holdout = UNSET
        else:
            holdout = FlagHoldout.from_dict(_holdout)

        _layers = d.pop("layers", UNSET)
        layers: list[FlagLayer] | Unset = UNSET
        if _layers is not UNSET:
            layers = []
            for layers_item_data in _layers:
                layers_item = FlagLayer.from_dict(layers_item_data)

                layers.append(layers_item)

        flags_config = cls(
            flags=flags,
            groups=groups,
            holdout=holdout,
            layers=layers,
        )

        return flags_config
