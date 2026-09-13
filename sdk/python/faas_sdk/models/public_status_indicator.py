from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.public_status_indicator_comparison import (
    PublicStatusIndicatorComparison,
    check_public_status_indicator_comparison,
)
from ..models.public_status_indicator_id import PublicStatusIndicatorId, check_public_status_indicator_id

T = TypeVar("T", bound="PublicStatusIndicator")


@_attrs_define
class PublicStatusIndicator:
    """Current error-budget indicator with its unit, target, and comparison direction."""

    id: PublicStatusIndicatorId
    label: str
    value: float | None
    unit: str
    target: float
    comparison: PublicStatusIndicatorComparison

    def to_dict(self) -> dict[str, Any]:
        id: str = self.id

        label = self.label

        value: float | None
        value = self.value

        unit = self.unit

        target = self.target

        comparison: str = self.comparison

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "label": label,
                "value": value,
                "unit": unit,
                "target": target,
                "comparison": comparison,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = check_public_status_indicator_id(d.pop("id"))

        label = d.pop("label")

        def _parse_value(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        value = _parse_value(d.pop("value"))

        unit = d.pop("unit")

        target = d.pop("target")

        comparison = check_public_status_indicator_comparison(d.pop("comparison"))

        public_status_indicator = cls(
            id=id,
            label=label,
            value=value,
            unit=unit,
            target=target,
            comparison=comparison,
        )

        return public_status_indicator
