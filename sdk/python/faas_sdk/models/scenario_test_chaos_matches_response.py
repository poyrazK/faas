from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_test_chaos_match import ScenarioTestChaosMatch


T = TypeVar("T", bound="ScenarioTestChaosMatchesResponse")


@_attrs_define
class ScenarioTestChaosMatchesResponse:
    """Run-scoped counts of HTTP requests and TCP connections that matched the current or most recently cleared plan."""

    matches: list[ScenarioTestChaosMatch]
    generation: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        matches = []
        for matches_item_data in self.matches:
            matches_item = matches_item_data.to_dict()
            matches.append(matches_item)

        generation: str | Unset = UNSET
        if not isinstance(self.generation, Unset):
            generation = str(self.generation)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "matches": matches,
            }
        )
        if generation is not UNSET:
            field_dict["generation"] = generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_test_chaos_match import ScenarioTestChaosMatch

        d = dict(src_dict)
        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = ScenarioTestChaosMatch.from_dict(matches_item_data)

            matches.append(matches_item)

        _generation = d.pop("generation", UNSET)
        generation: UUID | Unset
        if isinstance(_generation, Unset):
            generation = UNSET
        else:
            generation = UUID(_generation)

        scenario_test_chaos_matches_response = cls(
            matches=matches,
            generation=generation,
        )

        scenario_test_chaos_matches_response.additional_properties = d
        return scenario_test_chaos_matches_response

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
