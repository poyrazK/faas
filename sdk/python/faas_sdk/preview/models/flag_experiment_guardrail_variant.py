from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_guardrail_variant_status import (
    FlagExperimentGuardrailVariantStatus,
    check_flag_experiment_guardrail_variant_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExperimentGuardrailVariant")


@_attrs_define
class FlagExperimentGuardrailVariant:
    """One variant's guardrail rate against control's; difference is variant minus control, bounded by a 99% confidence
    sequence.

    """

    variant: str
    control_rate: float
    variant_rate: float
    control_units: int
    variant_units: int
    status: FlagExperimentGuardrailVariantStatus
    difference: float | Unset = UNSET
    low: float | Unset = UNSET
    high: float | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        control_rate = self.control_rate

        variant_rate = self.variant_rate

        control_units = self.control_units

        variant_units = self.variant_units

        status: str = self.status

        difference = self.difference

        low = self.low

        high = self.high

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "variant": variant,
                "control_rate": control_rate,
                "variant_rate": variant_rate,
                "control_units": control_units,
                "variant_units": variant_units,
                "status": status,
            }
        )
        if difference is not UNSET:
            field_dict["difference"] = difference
        if low is not UNSET:
            field_dict["low"] = low
        if high is not UNSET:
            field_dict["high"] = high

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant = d.pop("variant")

        control_rate = d.pop("control_rate")

        variant_rate = d.pop("variant_rate")

        control_units = d.pop("control_units")

        variant_units = d.pop("variant_units")

        status = check_flag_experiment_guardrail_variant_status(d.pop("status"))

        difference = d.pop("difference", UNSET)

        low = d.pop("low", UNSET)

        high = d.pop("high", UNSET)

        flag_experiment_guardrail_variant = cls(
            variant=variant,
            control_rate=control_rate,
            variant_rate=variant_rate,
            control_units=control_units,
            variant_units=variant_units,
            status=status,
            difference=difference,
            low=low,
            high=high,
        )

        flag_experiment_guardrail_variant.additional_properties = d
        return flag_experiment_guardrail_variant

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
