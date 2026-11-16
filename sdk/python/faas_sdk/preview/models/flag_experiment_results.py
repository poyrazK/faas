from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment import FlagExperiment
    from ..models.flag_experiment_arm import FlagExperimentArm
    from ..models.flag_experiment_variant_result import FlagExperimentVariantResult


T = TypeVar("T", bound="FlagExperimentResults")


@_attrs_define
class FlagExperimentResults:
    """A flag experiment's sequential results."""

    experiment: FlagExperiment
    """A flag experiment."""
    control: FlagExperimentArm
    """One variant's exposed and converted customers."""
    variants: list[FlagExperimentVariantResult]
    sample_ratio_p_value: float
    sample_ratio_mismatch: bool
    crossed_units: int
    """Customers that saw more than one variant and are left out."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        experiment = self.experiment.to_dict()

        control = self.control.to_dict()

        variants = []
        for variants_item_data in self.variants:
            variants_item = variants_item_data.to_dict()
            variants.append(variants_item)

        sample_ratio_p_value = self.sample_ratio_p_value

        sample_ratio_mismatch = self.sample_ratio_mismatch

        crossed_units = self.crossed_units

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "experiment": experiment,
                "control": control,
                "variants": variants,
                "sample_ratio_p_value": sample_ratio_p_value,
                "sample_ratio_mismatch": sample_ratio_mismatch,
                "crossed_units": crossed_units,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment import FlagExperiment
        from ..models.flag_experiment_arm import FlagExperimentArm
        from ..models.flag_experiment_variant_result import FlagExperimentVariantResult

        d = dict(src_dict)
        experiment = FlagExperiment.from_dict(d.pop("experiment"))

        control = FlagExperimentArm.from_dict(d.pop("control"))

        variants = []
        _variants = d.pop("variants")
        for variants_item_data in _variants:
            variants_item = FlagExperimentVariantResult.from_dict(variants_item_data)

            variants.append(variants_item)

        sample_ratio_p_value = d.pop("sample_ratio_p_value")

        sample_ratio_mismatch = d.pop("sample_ratio_mismatch")

        crossed_units = d.pop("crossed_units")

        flag_experiment_results = cls(
            experiment=experiment,
            control=control,
            variants=variants,
            sample_ratio_p_value=sample_ratio_p_value,
            sample_ratio_mismatch=sample_ratio_mismatch,
            crossed_units=crossed_units,
        )

        flag_experiment_results.additional_properties = d
        return flag_experiment_results

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
