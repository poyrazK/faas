from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_simulation_plan_version import (
    ScenarioSimulationPlanVersion,
    check_scenario_simulation_plan_version,
)

if TYPE_CHECKING:
    from ..models.scenario_simulation_fixture import ScenarioSimulationFixture


T = TypeVar("T", bound="ScenarioSimulationPlan")


@_attrs_define
class ScenarioSimulationPlan:
    """Immutable fixtures for contained serving apps using the managed outbound relay. Requires action evidence version 2.
    Canonical JSON is bounded to 64 KiB. No live provider fallback.

    """

    version: ScenarioSimulationPlanVersion
    fixtures: list[ScenarioSimulationFixture]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        fixtures = []
        for fixtures_item_data in self.fixtures:
            fixtures_item = fixtures_item_data.to_dict()
            fixtures.append(fixtures_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "fixtures": fixtures,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_simulation_fixture import ScenarioSimulationFixture

        d = dict(src_dict)
        version = check_scenario_simulation_plan_version(d.pop("version"))

        fixtures = []
        _fixtures = d.pop("fixtures")
        for fixtures_item_data in _fixtures:
            fixtures_item = ScenarioSimulationFixture.from_dict(fixtures_item_data)

            fixtures.append(fixtures_item)

        scenario_simulation_plan = cls(
            version=version,
            fixtures=fixtures,
        )

        scenario_simulation_plan.additional_properties = d
        return scenario_simulation_plan

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
