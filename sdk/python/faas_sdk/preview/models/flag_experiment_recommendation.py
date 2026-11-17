from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_experiment_recommendation_action import (
    FlagExperimentRecommendationAction,
    check_flag_experiment_recommendation_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagExperimentRecommendation")


@_attrs_define
class FlagExperimentRecommendation:
    """What to do with the results; commands carry it out. Gregale never ships a variant on its own."""

    action: FlagExperimentRecommendationAction
    summary: str
    variant: str | Unset = UNSET
    """The variant to ship for ship_variant."""
    commands: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        summary = self.summary

        variant = self.variant

        commands: list[str] | Unset = UNSET
        if not isinstance(self.commands, Unset):
            commands = self.commands

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "summary": summary,
            }
        )
        if variant is not UNSET:
            field_dict["variant"] = variant
        if commands is not UNSET:
            field_dict["commands"] = commands

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_flag_experiment_recommendation_action(d.pop("action"))

        summary = d.pop("summary")

        variant = d.pop("variant", UNSET)

        commands = cast(list[str], d.pop("commands", UNSET))

        flag_experiment_recommendation = cls(
            action=action,
            summary=summary,
            variant=variant,
            commands=commands,
        )

        flag_experiment_recommendation.additional_properties = d
        return flag_experiment_recommendation

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
