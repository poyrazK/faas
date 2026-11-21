from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment_layer_slice import FlagExperimentLayerSlice


T = TypeVar("T", bound="FlagExperimentLayer")


@_attrs_define
class FlagExperimentLayer:
    """An experiment layer and the slices its running experiments hold."""

    key: str
    slices: list[FlagExperimentLayerSlice]
    free: int
    """Basis points no experiment holds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        slices = []
        for slices_item_data in self.slices:
            slices_item = slices_item_data.to_dict()
            slices.append(slices_item)

        free = self.free

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "slices": slices,
                "free": free,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_layer_slice import FlagExperimentLayerSlice

        d = dict(src_dict)
        key = d.pop("key")

        slices = []
        _slices = d.pop("slices")
        for slices_item_data in _slices:
            slices_item = FlagExperimentLayerSlice.from_dict(slices_item_data)

            slices.append(slices_item)

        free = d.pop("free")

        flag_experiment_layer = cls(
            key=key,
            slices=slices,
            free=free,
        )

        flag_experiment_layer.additional_properties = d
        return flag_experiment_layer

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
