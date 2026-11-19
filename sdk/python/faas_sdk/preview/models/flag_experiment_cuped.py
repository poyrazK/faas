from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_cuped_reason import FlagExperimentCupedReason, check_flag_experiment_cuped_reason
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExperimentCuped")


@_attrs_define
class FlagExperimentCuped:
    """CUPED variance reduction on the results; reason says why it was not applied."""

    applied: bool
    pre_days: int
    """Days before the start that make each customer's covariate."""
    theta: float | Unset = UNSET
    variance_reduction: float | Unset = UNSET
    """Share of the metric's variance removed, 0 to 1."""
    reason: FlagExperimentCupedReason | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        applied = self.applied

        pre_days = self.pre_days

        theta = self.theta

        variance_reduction = self.variance_reduction

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "applied": applied,
                "pre_days": pre_days,
            }
        )
        if theta is not UNSET:
            field_dict["theta"] = theta
        if variance_reduction is not UNSET:
            field_dict["variance_reduction"] = variance_reduction
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        applied = d.pop("applied")

        pre_days = d.pop("pre_days")

        theta = d.pop("theta", UNSET)

        variance_reduction = d.pop("variance_reduction", UNSET)

        _reason = d.pop("reason", UNSET)
        reason: FlagExperimentCupedReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_flag_experiment_cuped_reason(_reason)

        flag_experiment_cuped = cls(
            applied=applied,
            pre_days=pre_days,
            theta=theta,
            variance_reduction=variance_reduction,
            reason=reason,
        )

        flag_experiment_cuped.additional_properties = d
        return flag_experiment_cuped

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
