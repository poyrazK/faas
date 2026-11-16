from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExperimentCreateRequest")


@_attrs_define
class FlagExperimentCreateRequest:
    """A flag experiment to create."""

    key: str
    flag: str
    """The variant flag."""
    rule_id: str
    """The flag's weighted rule."""
    control: str
    """The baseline variant."""
    metric_event: str
    """The conversion event."""
    mixing_lift: float | Unset = UNSET
    """mSPRT mixing standard deviation as an absolute conversion-rate lift; default 0.02."""
    start: bool | Unset = UNSET
    """Start the experiment at once."""

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        flag = self.flag

        rule_id = self.rule_id

        control = self.control

        metric_event = self.metric_event

        mixing_lift = self.mixing_lift

        start = self.start

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "flag": flag,
                "rule_id": rule_id,
                "control": control,
                "metric_event": metric_event,
            }
        )
        if mixing_lift is not UNSET:
            field_dict["mixing_lift"] = mixing_lift
        if start is not UNSET:
            field_dict["start"] = start

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        flag = d.pop("flag")

        rule_id = d.pop("rule_id")

        control = d.pop("control")

        metric_event = d.pop("metric_event")

        mixing_lift = d.pop("mixing_lift", UNSET)

        start = d.pop("start", UNSET)

        flag_experiment_create_request = cls(
            key=key,
            flag=flag,
            rule_id=rule_id,
            control=control,
            metric_event=metric_event,
            mixing_lift=mixing_lift,
            start=start,
        )

        return flag_experiment_create_request
