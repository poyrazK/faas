from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="InjectScenarioTestChaosResponse")


@_attrs_define
class InjectScenarioTestChaosResponse:
    """Receipt confirming the plan generation, number of rules installed, and automatic expiry time."""

    expires_at: datetime.datetime
    rules_installed: int
    generation: UUID
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        expires_at = self.expires_at.isoformat()

        rules_installed = self.rules_installed

        generation = str(self.generation)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "expires_at": expires_at,
                "rules_installed": rules_installed,
                "generation": generation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        rules_installed = d.pop("rules_installed")

        generation = UUID(d.pop("generation"))

        inject_scenario_test_chaos_response = cls(
            expires_at=expires_at,
            rules_installed=rules_installed,
            generation=generation,
        )

        inject_scenario_test_chaos_response.additional_properties = d
        return inject_scenario_test_chaos_response

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
