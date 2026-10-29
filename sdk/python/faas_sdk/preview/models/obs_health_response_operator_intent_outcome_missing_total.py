from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObsHealthResponseOperatorIntentOutcomeMissingTotal")


@_attrs_define
class ObsHealthResponseOperatorIntentOutcomeMissingTotal:
    """Per-kind count of operator_intents rows stuck in
    `running` past the 5m threshold. The handler seeds
    every kind in the operator-action vocabulary
    (force_park, force_cold_boot, force_restart) with
    0 so the JSON shape stays stable.

        Example:
            {'force_park': 0, 'force_cold_boot': 0, 'force_restart': 0}

    """

    additional_properties: dict[str, int] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        obs_health_response_operator_intent_outcome_missing_total = cls()

        obs_health_response_operator_intent_outcome_missing_total.additional_properties = d
        return obs_health_response_operator_intent_outcome_missing_total

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> int:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: int) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
