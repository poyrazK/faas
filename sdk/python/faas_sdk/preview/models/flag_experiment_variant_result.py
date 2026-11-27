from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_variant_result_status import (
    FlagExperimentVariantResultStatus,
    check_flag_experiment_variant_result_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment_comparison import FlagExperimentComparison


T = TypeVar("T", bound="FlagExperimentVariantResult")


@_attrs_define
class FlagExperimentVariantResult:
    """One variant compared with control."""

    variant: str
    exposed: int
    converted: int
    rate: float
    status: FlagExperimentVariantResultStatus
    lift: float | Unset = UNSET
    """Absolute difference in conversion rate against control."""
    relative_lift: float | Unset = UNSET
    low: float | Unset = UNSET
    """Lower bound of the 95% confidence sequence on lift."""
    high: float | Unset = UNSET
    """Upper bound of the 95% confidence sequence on lift."""
    p_value: float | Unset = UNSET
    """Always-valid p-value (running minimum)."""
    unadjusted: FlagExperimentComparison | Unset = UNSET
    """A variant's comparison with control without CUPED."""
    best_probability: float | Unset = UNSET
    """A bandit arm's posterior probability of being best; bandits report no p-values."""
    expected_loss: float | Unset = UNSET
    """Conversion rate a bandit gives up by choosing this arm."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        variant = self.variant

        exposed = self.exposed

        converted = self.converted

        rate = self.rate

        status: str = self.status

        lift = self.lift

        relative_lift = self.relative_lift

        low = self.low

        high = self.high

        p_value = self.p_value

        unadjusted: dict[str, Any] | Unset = UNSET
        if not isinstance(self.unadjusted, Unset):
            unadjusted = self.unadjusted.to_dict()

        best_probability = self.best_probability

        expected_loss = self.expected_loss

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "variant": variant,
                "exposed": exposed,
                "converted": converted,
                "rate": rate,
                "status": status,
            }
        )
        if lift is not UNSET:
            field_dict["lift"] = lift
        if relative_lift is not UNSET:
            field_dict["relative_lift"] = relative_lift
        if low is not UNSET:
            field_dict["low"] = low
        if high is not UNSET:
            field_dict["high"] = high
        if p_value is not UNSET:
            field_dict["p_value"] = p_value
        if unadjusted is not UNSET:
            field_dict["unadjusted"] = unadjusted
        if best_probability is not UNSET:
            field_dict["best_probability"] = best_probability
        if expected_loss is not UNSET:
            field_dict["expected_loss"] = expected_loss

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_comparison import FlagExperimentComparison

        d = dict(src_dict)
        variant = d.pop("variant")

        exposed = d.pop("exposed")

        converted = d.pop("converted")

        rate = d.pop("rate")

        status = check_flag_experiment_variant_result_status(d.pop("status"))

        lift = d.pop("lift", UNSET)

        relative_lift = d.pop("relative_lift", UNSET)

        low = d.pop("low", UNSET)

        high = d.pop("high", UNSET)

        p_value = d.pop("p_value", UNSET)

        _unadjusted = d.pop("unadjusted", UNSET)
        unadjusted: FlagExperimentComparison | Unset
        if isinstance(_unadjusted, Unset):
            unadjusted = UNSET
        else:
            unadjusted = FlagExperimentComparison.from_dict(_unadjusted)

        best_probability = d.pop("best_probability", UNSET)

        expected_loss = d.pop("expected_loss", UNSET)

        flag_experiment_variant_result = cls(
            variant=variant,
            exposed=exposed,
            converted=converted,
            rate=rate,
            status=status,
            lift=lift,
            relative_lift=relative_lift,
            low=low,
            high=high,
            p_value=p_value,
            unadjusted=unadjusted,
            best_probability=best_probability,
            expected_loss=expected_loss,
        )

        flag_experiment_variant_result.additional_properties = d
        return flag_experiment_variant_result

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
