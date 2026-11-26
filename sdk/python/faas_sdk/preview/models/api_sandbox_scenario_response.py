from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_scenario import APISandboxScenario


T = TypeVar("T", bound="APISandboxScenarioResponse")


@_attrs_define
class APISandboxScenarioResponse:
    """A release's current scenario and optional source request identifier."""

    scenario: APISandboxScenario | None
    source_request_id: UUID | Unset = UNSET
    """Private Debugger provenance, present when the saved scenario originated from a retained request."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.api_sandbox_scenario import APISandboxScenario

        scenario: dict[str, Any] | None
        if isinstance(self.scenario, APISandboxScenario):
            scenario = self.scenario.to_dict()
        else:
            scenario = self.scenario

        source_request_id: str | Unset = UNSET
        if not isinstance(self.source_request_id, Unset):
            source_request_id = str(self.source_request_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scenario": scenario,
            }
        )
        if source_request_id is not UNSET:
            field_dict["source_request_id"] = source_request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_scenario import APISandboxScenario

        d = dict(src_dict)

        def _parse_scenario(data: object) -> APISandboxScenario | None:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                scenario_type_0 = APISandboxScenario.from_dict(data)

                return scenario_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(APISandboxScenario | None, data)

        scenario = _parse_scenario(d.pop("scenario"))

        _source_request_id = d.pop("source_request_id", UNSET)
        source_request_id: UUID | Unset
        if isinstance(_source_request_id, Unset):
            source_request_id = UNSET
        else:
            source_request_id = UUID(_source_request_id)

        api_sandbox_scenario_response = cls(
            scenario=scenario,
            source_request_id=source_request_id,
        )

        api_sandbox_scenario_response.additional_properties = d
        return api_sandbox_scenario_response

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
