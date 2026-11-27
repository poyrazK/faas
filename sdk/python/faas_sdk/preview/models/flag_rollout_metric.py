from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagRolloutMetric")


@_attrs_define
class FlagRolloutMetric:
    """Gates an automatic stage on a conversion event: customers the stage serves are compared with every other customer
    that evaluated the flag. The stage rolls back to 0 when their rate is clearly more than max_decrease lower, and
    advances only with min_units in each group and no such decrease.

    """

    event: str
    max_decrease: float | Unset = UNSET
    """Tolerated absolute conversion rate decrease; 0 means 0.01."""
    min_units: int | Unset = UNSET
    """Customers each group needs before a stage advances; 0 means 100."""

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        max_decrease = self.max_decrease

        min_units = self.min_units

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event": event,
            }
        )
        if max_decrease is not UNSET:
            field_dict["max_decrease"] = max_decrease
        if min_units is not UNSET:
            field_dict["min_units"] = min_units

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event = d.pop("event")

        max_decrease = d.pop("max_decrease", UNSET)

        min_units = d.pop("min_units", UNSET)

        flag_rollout_metric = cls(
            event=event,
            max_decrease=max_decrease,
            min_units=min_units,
        )

        return flag_rollout_metric
