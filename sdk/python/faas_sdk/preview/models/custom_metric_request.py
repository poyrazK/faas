from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CustomMetricRequest")


@_attrs_define
class CustomMetricRequest:
    """ADR-202 push body. The metric name travels in the URL path, so the body carries only the number that varies — which
    is what makes the push idempotent by construction.

    """

    value: float
    """Fleet-total quantity one instance should carry. The scheduler computes ceil(value / target). Must be finite,
    >= 0, and <= 1e12; a larger reading is a broken producer, and without the bound one bad push would demand the
    plan cap's worth of instances on the very next tick."""

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "value": value,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        value = d.pop("value")

        custom_metric_request = cls(
            value=value,
        )

        return custom_metric_request
