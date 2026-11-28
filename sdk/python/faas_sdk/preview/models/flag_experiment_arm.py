from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagExperimentArm")


@_attrs_define
class FlagExperimentArm:
    """One variant's exposed customers; rate is the conversion rate, or the mean value per customer for mean metrics."""

    variant: str
    exposed: int
    converted: int
    rate: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        exposed = self.exposed

        converted = self.converted

        rate = self.rate

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "variant": variant,
                "exposed": exposed,
                "converted": converted,
                "rate": rate,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant = d.pop("variant")

        exposed = d.pop("exposed")

        converted = d.pop("converted")

        rate = d.pop("rate")

        flag_experiment_arm = cls(
            variant=variant,
            exposed=exposed,
            converted=converted,
            rate=rate,
        )

        flag_experiment_arm.additional_properties = d
        return flag_experiment_arm

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
