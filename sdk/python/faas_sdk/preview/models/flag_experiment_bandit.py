from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_experiment_bandit_arm import FlagExperimentBanditArm


T = TypeVar("T", bound="FlagExperimentBandit")


@_attrs_define
class FlagExperimentBandit:
    """A bandit's posterior per arm and when it last re-weighted the rule."""

    arms: list[FlagExperimentBanditArm]
    weighted_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        arms = []
        for arms_item_data in self.arms:
            arms_item = arms_item_data.to_dict()
            arms.append(arms_item)

        weighted_at: str | Unset = UNSET
        if not isinstance(self.weighted_at, Unset):
            weighted_at = self.weighted_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "arms": arms,
            }
        )
        if weighted_at is not UNSET:
            field_dict["weighted_at"] = weighted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_experiment_bandit_arm import FlagExperimentBanditArm

        d = dict(src_dict)
        arms = []
        _arms = d.pop("arms")
        for arms_item_data in _arms:
            arms_item = FlagExperimentBanditArm.from_dict(arms_item_data)

            arms.append(arms_item)

        _weighted_at = d.pop("weighted_at", UNSET)
        weighted_at: datetime.datetime | Unset
        if isinstance(_weighted_at, Unset):
            weighted_at = UNSET
        else:
            weighted_at = datetime.datetime.fromisoformat(_weighted_at)

        flag_experiment_bandit = cls(
            arms=arms,
            weighted_at=weighted_at,
        )

        flag_experiment_bandit.additional_properties = d
        return flag_experiment_bandit

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
