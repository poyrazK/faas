from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectRetentionPeriod")


@_attrs_define
class ObjectRetentionPeriod:
    """Exactly one positive days or years duration must be present. Null values are rejected."""

    days: int | Unset = UNSET
    years: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        days = self.days

        years = self.years

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if days is not UNSET:
            field_dict["days"] = days
        if years is not UNSET:
            field_dict["years"] = years

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        days = d.pop("days", UNSET)

        years = d.pop("years", UNSET)

        object_retention_period = cls(
            days=days,
            years=years,
        )

        return object_retention_period
