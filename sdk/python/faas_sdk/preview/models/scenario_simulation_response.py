from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_simulation_response_fault import (
    ScenarioSimulationResponseFault,
    check_scenario_simulation_response_fault,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioSimulationResponse")


@_attrs_define
class ScenarioSimulationResponse:
    """Deterministic JSON HTTP response or connection fault. Redirects are refused. Timeout requires a positive bounded
    delay, then closes the connection without an HTTP response. This does not attest which error the application
    observed.

    """

    status: int | Unset = UNSET
    """Required for HTTP responses. Status 300 through 399 is unsupported. Absent for faults."""
    body: str | Unset = UNSET
    """Literal JSON response body. Absent for faults."""
    retry_after_seconds: int | Unset = UNSET
    """Supported with status 429 or 503 only."""
    fault: ScenarioSimulationResponseFault | Unset = UNSET
    delay_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        body = self.body

        retry_after_seconds = self.retry_after_seconds

        fault: str | Unset = UNSET
        if not isinstance(self.fault, Unset):
            fault = self.fault

        delay_ms = self.delay_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if status is not UNSET:
            field_dict["status"] = status
        if body is not UNSET:
            field_dict["body"] = body
        if retry_after_seconds is not UNSET:
            field_dict["retry_after_seconds"] = retry_after_seconds
        if fault is not UNSET:
            field_dict["fault"] = fault
        if delay_ms is not UNSET:
            field_dict["delay_ms"] = delay_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status", UNSET)

        body = d.pop("body", UNSET)

        retry_after_seconds = d.pop("retry_after_seconds", UNSET)

        _fault = d.pop("fault", UNSET)
        fault: ScenarioSimulationResponseFault | Unset
        if isinstance(_fault, Unset):
            fault = UNSET
        else:
            fault = check_scenario_simulation_response_fault(_fault)

        delay_ms = d.pop("delay_ms", UNSET)

        scenario_simulation_response = cls(
            status=status,
            body=body,
            retry_after_seconds=retry_after_seconds,
            fault=fault,
            delay_ms=delay_ms,
        )

        scenario_simulation_response.additional_properties = d
        return scenario_simulation_response

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
