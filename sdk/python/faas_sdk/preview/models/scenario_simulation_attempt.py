from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_simulation_attempt_adapter import (
    ScenarioSimulationAttemptAdapter,
    check_scenario_simulation_attempt_adapter,
)
from ..models.scenario_simulation_attempt_fault import (
    ScenarioSimulationAttemptFault,
    check_scenario_simulation_attempt_fault,
)
from ..models.scenario_simulation_attempt_status import (
    ScenarioSimulationAttemptStatus,
    check_scenario_simulation_attempt_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioSimulationAttempt")


@_attrs_define
class ScenarioSimulationAttempt:
    """Durable simulated relay attempt recorded before response delivery. This is not proof of email delivery or payment.
    Headers are excluded. Payload contains declared JSON fields. Hashes identify the canonical full request and
    idempotency key.

    """

    id: UUID
    workload: str
    fixture: str
    integration: str
    adapter: ScenarioSimulationAttemptAdapter
    method: str
    path: str
    request_sha256: str
    payload: Any
    """Captured top-level JSON fields selected by the fixture."""
    attempt: int
    status: ScenarioSimulationAttemptStatus
    response_status: int
    idempotency_sha256: str | Unset = UNSET
    fault: ScenarioSimulationAttemptFault | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        fixture = self.fixture

        integration = self.integration

        adapter: str = self.adapter

        method = self.method

        path = self.path

        request_sha256 = self.request_sha256

        payload = self.payload

        attempt = self.attempt

        status: str = self.status

        response_status = self.response_status

        idempotency_sha256 = self.idempotency_sha256

        fault: str | Unset = UNSET
        if not isinstance(self.fault, Unset):
            fault = self.fault

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "fixture": fixture,
                "integration": integration,
                "adapter": adapter,
                "method": method,
                "path": path,
                "request_sha256": request_sha256,
                "payload": payload,
                "attempt": attempt,
                "status": status,
                "response_status": response_status,
            }
        )
        if idempotency_sha256 is not UNSET:
            field_dict["idempotency_sha256"] = idempotency_sha256
        if fault is not UNSET:
            field_dict["fault"] = fault

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        fixture = d.pop("fixture")

        integration = d.pop("integration")

        adapter = check_scenario_simulation_attempt_adapter(d.pop("adapter"))

        method = d.pop("method")

        path = d.pop("path")

        request_sha256 = d.pop("request_sha256")

        payload = d.pop("payload")

        attempt = d.pop("attempt")

        status = check_scenario_simulation_attempt_status(d.pop("status"))

        response_status = d.pop("response_status")

        idempotency_sha256 = d.pop("idempotency_sha256", UNSET)

        _fault = d.pop("fault", UNSET)
        fault: ScenarioSimulationAttemptFault | Unset
        if isinstance(_fault, Unset):
            fault = UNSET
        else:
            fault = check_scenario_simulation_attempt_fault(_fault)

        scenario_simulation_attempt = cls(
            id=id,
            workload=workload,
            fixture=fixture,
            integration=integration,
            adapter=adapter,
            method=method,
            path=path,
            request_sha256=request_sha256,
            payload=payload,
            attempt=attempt,
            status=status,
            response_status=response_status,
            idempotency_sha256=idempotency_sha256,
            fault=fault,
        )

        scenario_simulation_attempt.additional_properties = d
        return scenario_simulation_attempt

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
