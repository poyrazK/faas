from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment_layer import FlagExperimentLayer


T = TypeVar("T", bound="FlagExperimentLayerList")


@_attrs_define
class FlagExperimentLayerList:
    """An environment's experiment layers."""

    layers: list[FlagExperimentLayer]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        layers = []
        for layers_item_data in self.layers:
            layers_item = layers_item_data.to_dict()
            layers.append(layers_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "layers": layers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_layer import FlagExperimentLayer

        d = dict(src_dict)
        layers = []
        _layers = d.pop("layers")
        for layers_item_data in _layers:
            layers_item = FlagExperimentLayer.from_dict(layers_item_data)

            layers.append(layers_item)

        flag_experiment_layer_list = cls(
            layers=layers,
        )

        flag_experiment_layer_list.additional_properties = d
        return flag_experiment_layer_list

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
