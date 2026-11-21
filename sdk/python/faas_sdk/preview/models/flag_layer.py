from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagLayer")


@_attrs_define
class FlagLayer:
    """An exclusive experiment layer: experiments in one layer hold disjoint slices of its customers (or subjects)."""

    key: str
    seed: str

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        seed = self.seed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "seed": seed,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        seed = d.pop("seed")

        flag_layer = cls(
            key=key,
            seed=seed,
        )

        return flag_layer
