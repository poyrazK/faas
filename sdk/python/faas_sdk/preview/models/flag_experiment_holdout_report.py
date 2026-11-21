from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.flag_experiment_arm import FlagExperimentArm


T = TypeVar("T", bound="FlagExperimentHoldoutReport")


@_attrs_define
class FlagExperimentHoldoutReport:
    """Held-out customers against every other customer active in the range: the share that sent the event, their difference
    (exposed minus held) and a 95% interval.

    """

    event: str
    from_: datetime.datetime
    until: datetime.datetime
    basis_points: int
    held: FlagExperimentArm
    """One variant's exposed customers; rate is the conversion rate, or the mean value per customer for mean
    metrics."""
    exposed: FlagExperimentArm
    """One variant's exposed customers; rate is the conversion rate, or the mean value per customer for mean
    metrics."""
    difference: float
    low: float
    high: float
    truncated: bool
    """True when the customer lists hit the analytics unit bound."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        basis_points = self.basis_points

        held = self.held.to_dict()

        exposed = self.exposed.to_dict()

        difference = self.difference

        low = self.low

        high = self.high

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event": event,
                "from": from_,
                "until": until,
                "basis_points": basis_points,
                "held": held,
                "exposed": exposed,
                "difference": difference,
                "low": low,
                "high": high,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_arm import FlagExperimentArm

        d = dict(src_dict)
        event = d.pop("event")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        basis_points = d.pop("basis_points")

        held = FlagExperimentArm.from_dict(d.pop("held"))

        exposed = FlagExperimentArm.from_dict(d.pop("exposed"))

        difference = d.pop("difference")

        low = d.pop("low")

        high = d.pop("high")

        truncated = d.pop("truncated")

        flag_experiment_holdout_report = cls(
            event=event,
            from_=from_,
            until=until,
            basis_points=basis_points,
            held=held,
            exposed=exposed,
            difference=difference,
            low=low,
            high=high,
            truncated=truncated,
        )

        flag_experiment_holdout_report.additional_properties = d
        return flag_experiment_holdout_report

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
