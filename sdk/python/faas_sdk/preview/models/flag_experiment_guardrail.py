from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_guardrail_name import FlagExperimentGuardrailName, check_flag_experiment_guardrail_name
from ..models.flag_experiment_guardrail_status import (
    FlagExperimentGuardrailStatus,
    check_flag_experiment_guardrail_status,
)

if TYPE_CHECKING:
    from ..models.flag_experiment_guardrail_variant import FlagExperimentGuardrailVariant


T = TypeVar("T", bound="FlagExperimentGuardrail")


@_attrs_define
class FlagExperimentGuardrail:
    """One guardrail across the variants."""

    name: FlagExperimentGuardrailName
    tolerance: float
    status: FlagExperimentGuardrailStatus
    variants: list[FlagExperimentGuardrailVariant]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name: str = self.name

        tolerance = self.tolerance

        status: str = self.status

        variants = []
        for variants_item_data in self.variants:
            variants_item = variants_item_data.to_dict()
            variants.append(variants_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "tolerance": tolerance,
                "status": status,
                "variants": variants,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_guardrail_variant import FlagExperimentGuardrailVariant

        d = dict(src_dict)
        name = check_flag_experiment_guardrail_name(d.pop("name"))

        tolerance = d.pop("tolerance")

        status = check_flag_experiment_guardrail_status(d.pop("status"))

        variants = []
        _variants = d.pop("variants")
        for variants_item_data in _variants:
            variants_item = FlagExperimentGuardrailVariant.from_dict(variants_item_data)

            variants.append(variants_item)

        flag_experiment_guardrail = cls(
            name=name,
            tolerance=tolerance,
            status=status,
            variants=variants,
        )

        flag_experiment_guardrail.additional_properties = d
        return flag_experiment_guardrail

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
