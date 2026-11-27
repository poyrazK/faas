from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.feature_flag import FeatureFlag
    from ..models.flag_holdout import FlagHoldout
    from ..models.flag_layer import FlagLayer
    from ..models.flags_config_groups import FlagsConfigGroups


T = TypeVar("T", bound="FlagsBundle")


@_attrs_define
class FlagsBundle:
    """Immutable runtime configuration snapshot."""

    flags: list[FeatureFlag]
    groups: FlagsConfigGroups
    environment_id: UUID
    version: int
    holdout: FlagHoldout | Unset = UNSET
    """The environment holdout: basis_points of customers, chosen by seed, see every running experiment's control."""
    layers: list[FlagLayer] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flags = []
        for flags_item_data in self.flags:
            flags_item = flags_item_data.to_dict()
            flags.append(flags_item)

        groups = self.groups.to_dict()

        environment_id = str(self.environment_id)

        version = self.version

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
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flags": flags,
                "groups": groups,
                "environment_id": environment_id,
                "version": version,
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

        environment_id = UUID(d.pop("environment_id"))

        version = d.pop("version")

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

        flags_bundle = cls(
            flags=flags,
            groups=groups,
            environment_id=environment_id,
            version=version,
            holdout=holdout,
            layers=layers,
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
