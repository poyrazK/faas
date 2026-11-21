from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagExperimentHoldout")


@_attrs_define
class FlagExperimentHoldout:
    """The environment holdout: basis points of customers that see every experiment's control."""

    basis_points: int

    def to_dict(self) -> dict[str, Any]:
        basis_points = self.basis_points

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "basis_points": basis_points,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        basis_points = d.pop("basis_points")

        flag_experiment_holdout = cls(
            basis_points=basis_points,
        )

        return flag_experiment_holdout
