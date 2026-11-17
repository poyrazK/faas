from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment import FlagExperiment


T = TypeVar("T", bound="FlagExperimentList")


@_attrs_define
class FlagExperimentList:
    """An environment's flag experiments."""

    experiments: list[FlagExperiment]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        experiments = []
        for experiments_item_data in self.experiments:
            experiments_item = experiments_item_data.to_dict()
            experiments.append(experiments_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "experiments": experiments,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment import FlagExperiment

        d = dict(src_dict)
        experiments = []
        _experiments = d.pop("experiments")
        for experiments_item_data in _experiments:
            experiments_item = FlagExperiment.from_dict(experiments_item_data)

            experiments.append(experiments_item)

        flag_experiment_list = cls(
            experiments=experiments,
        )

        flag_experiment_list.additional_properties = d
        return flag_experiment_list

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
