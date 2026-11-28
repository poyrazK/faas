from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.flag_rule_experiment_unit import FlagRuleExperimentUnit, check_flag_rule_experiment_unit
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagRuleExperiment")


@_attrs_define
class FlagRuleExperiment:
    """The running experiment of a weighted rule, set when the experiment starts and cleared when it stops. Apid writes it;
    a configuration update must carry it unchanged. Runtime SDKs need capabilities experiment-layers-v1 and subject-
    exposures-v1.

    """

    key: str
    """Experiment key."""
    control: str
    """The variant held-out customers see."""
    unit: FlagRuleExperimentUnit
    layer: str | Unset = UNSET
    """Exclusive layer whose slice [from, to) the experiment holds."""
    from_: int | Unset = UNSET
    to: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        control = self.control

        unit: str = self.unit

        layer = self.layer

        from_ = self.from_

        to = self.to

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "key": key,
                "control": control,
                "unit": unit,
            }
        )
        if layer is not UNSET:
            field_dict["layer"] = layer
        if from_ is not UNSET:
            field_dict["from"] = from_
        if to is not UNSET:
            field_dict["to"] = to

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        control = d.pop("control")

        unit = check_flag_rule_experiment_unit(d.pop("unit"))

        layer = d.pop("layer", UNSET)

        from_ = d.pop("from", UNSET)

        to = d.pop("to", UNSET)

        flag_rule_experiment = cls(
            key=key,
            control=control,
            unit=unit,
            layer=layer,
            from_=from_,
            to=to,
        )

        return flag_rule_experiment
