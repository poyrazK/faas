from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagExperimentBanditArm")


@_attrs_define
class FlagExperimentBanditArm:
    """One bandit arm's served weight, best-arm probability and expected loss."""

    variant: str
    weight: int
    best_probability: float
    expected_loss: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        weight = self.weight

        best_probability = self.best_probability

        expected_loss = self.expected_loss

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "variant": variant,
                "weight": weight,
                "best_probability": best_probability,
                "expected_loss": expected_loss,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        variant = d.pop("variant")

        weight = d.pop("weight")

        best_probability = d.pop("best_probability")

        expected_loss = d.pop("expected_loss")

        flag_experiment_bandit_arm = cls(
            variant=variant,
            weight=weight,
            best_probability=best_probability,
            expected_loss=expected_loss,
        )

        flag_experiment_bandit_arm.additional_properties = d
        return flag_experiment_bandit_arm

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
