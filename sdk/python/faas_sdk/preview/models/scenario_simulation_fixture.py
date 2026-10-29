from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_simulation_fixture_adapter import (
    ScenarioSimulationFixtureAdapter,
    check_scenario_simulation_fixture_adapter,
)
from ..models.scenario_simulation_fixture_method import (
    ScenarioSimulationFixtureMethod,
    check_scenario_simulation_fixture_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_simulation_response import ScenarioSimulationResponse


T = TypeVar("T", bound="ScenarioSimulationFixture")


@_attrs_define
class ScenarioSimulationFixture:
    """One exact workload, integration, method and path. Responses advance per idempotency key, or per fixture without a
    key; the final response repeats. Only named top-level JSON fields are retained.

    """

    workload: str
    name: str
    integration: str
    adapter: ScenarioSimulationFixtureAdapter
    method: ScenarioSimulationFixtureMethod
    path: str
    """Exact origin path without query, fragment, percent encoding or traversal. Resend supports POST /emails."""
    responses: list[ScenarioSimulationResponse]
    capture_fields: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload = self.workload

        name = self.name

        integration = self.integration

        adapter: str = self.adapter

        method: str = self.method

        path = self.path

        responses = []
        for responses_item_data in self.responses:
            responses_item = responses_item_data.to_dict()
            responses.append(responses_item)

        capture_fields: list[str] | Unset = UNSET
        if not isinstance(self.capture_fields, Unset):
            capture_fields = self.capture_fields

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload": workload,
                "name": name,
                "integration": integration,
                "adapter": adapter,
                "method": method,
                "path": path,
                "responses": responses,
            }
        )
        if capture_fields is not UNSET:
            field_dict["capture_fields"] = capture_fields

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_simulation_response import ScenarioSimulationResponse

        d = dict(src_dict)
        workload = d.pop("workload")

        name = d.pop("name")

        integration = d.pop("integration")

        adapter = check_scenario_simulation_fixture_adapter(d.pop("adapter"))

        method = check_scenario_simulation_fixture_method(d.pop("method"))

        path = d.pop("path")

        responses = []
        _responses = d.pop("responses")
        for responses_item_data in _responses:
            responses_item = ScenarioSimulationResponse.from_dict(responses_item_data)

            responses.append(responses_item)

        capture_fields = cast(list[str], d.pop("capture_fields", UNSET))

        scenario_simulation_fixture = cls(
            workload=workload,
            name=name,
            integration=integration,
            adapter=adapter,
            method=method,
            path=path,
            responses=responses,
            capture_fields=capture_fields,
        )

        scenario_simulation_fixture.additional_properties = d
        return scenario_simulation_fixture

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
