from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagHoldout")


@_attrs_define
class FlagHoldout:
    """The environment holdout: basis_points of customers, chosen by seed, see every running experiment's control."""

    seed: str
    basis_points: int

    def to_dict(self) -> dict[str, Any]:
        seed = self.seed

        basis_points = self.basis_points

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "seed": seed,
                "basis_points": basis_points,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        seed = d.pop("seed")

        basis_points = d.pop("basis_points")

        flag_holdout = cls(
            seed=seed,
            basis_points=basis_points,
        )

        return flag_holdout
