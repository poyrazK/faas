from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="FlagVariant")


@_attrs_define
class FlagVariant:
    """Variant allocation weight in basis points; all weights on a flag total 10000."""

    key: str
    weight: int

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        weight = self.weight

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "weight": weight,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        weight = d.pop("weight")

        flag_variant = cls(
            key=key,
            weight=weight,
        )

        return flag_variant
