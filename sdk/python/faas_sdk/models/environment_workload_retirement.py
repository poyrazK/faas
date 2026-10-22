from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.environment_workload_retirement_phase import (
    EnvironmentWorkloadRetirementPhase,
    check_environment_workload_retirement_phase,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EnvironmentWorkloadRetirement")


@_attrs_define
class EnvironmentWorkloadRetirement:
    """Retained workload identity and durable route withdrawal and scheduler drain progress. Historical evidence does not
    grant execution authority.

    """

    id: UUID
    environment_id: UUID
    source_id: UUID
    app_id: UUID
    revision_id: UUID
    resource: str
    phase: EnvironmentWorkloadRetirementPhase
    generation: int
    requested_at: datetime.datetime
    account_id: UUID | Unset = UNSET
    project_id: UUID | Unset = UNSET
    plan_hash: str | Unset = UNSET
    job_id: str | Unset = UNSET
    route_deployment_id: str | Unset = UNSET
    recovery_revision_id: str | Unset = UNSET
    route_generation: int | Unset = UNSET
    recovery_generation: int | Unset = UNSET
    deployment_ids: list[str] | Unset = UNSET
    queue_binding_ids: list[str] | Unset = UNSET
    expected_gateways: list[str] | Unset = UNSET
    acknowledged_gateways: list[str] | Unset = UNSET
    retired_at: datetime.datetime | None | Unset = UNSET
    recovered_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        environment_id = str(self.environment_id)

        source_id = str(self.source_id)

        app_id = str(self.app_id)

        revision_id = str(self.revision_id)

        resource = self.resource

        phase: str = self.phase

        generation = self.generation

        requested_at = self.requested_at.isoformat()

        account_id: str | Unset = UNSET
        if not isinstance(self.account_id, Unset):
            account_id = str(self.account_id)

        project_id: str | Unset = UNSET
        if not isinstance(self.project_id, Unset):
            project_id = str(self.project_id)

        plan_hash = self.plan_hash

        job_id = self.job_id

        route_deployment_id = self.route_deployment_id

        recovery_revision_id = self.recovery_revision_id

        route_generation = self.route_generation

        recovery_generation = self.recovery_generation

        deployment_ids: list[str] | Unset = UNSET
        if not isinstance(self.deployment_ids, Unset):
            deployment_ids = self.deployment_ids

        queue_binding_ids: list[str] | Unset = UNSET
        if not isinstance(self.queue_binding_ids, Unset):
            queue_binding_ids = self.queue_binding_ids

        expected_gateways: list[str] | Unset = UNSET
        if not isinstance(self.expected_gateways, Unset):
            expected_gateways = self.expected_gateways

        acknowledged_gateways: list[str] | Unset = UNSET
        if not isinstance(self.acknowledged_gateways, Unset):
            acknowledged_gateways = self.acknowledged_gateways

        retired_at: None | str | Unset
        if isinstance(self.retired_at, Unset):
            retired_at = UNSET
        elif isinstance(self.retired_at, datetime.datetime):
            retired_at = self.retired_at.isoformat()
        else:
            retired_at = self.retired_at

        recovered_at: None | str | Unset
        if isinstance(self.recovered_at, Unset):
            recovered_at = UNSET
        elif isinstance(self.recovered_at, datetime.datetime):
            recovered_at = self.recovered_at.isoformat()
        else:
            recovered_at = self.recovered_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "environment_id": environment_id,
                "source_id": source_id,
                "app_id": app_id,
                "revision_id": revision_id,
                "resource": resource,
                "phase": phase,
                "generation": generation,
                "requested_at": requested_at,
            }
        )
        if account_id is not UNSET:
            field_dict["account_id"] = account_id
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if plan_hash is not UNSET:
            field_dict["plan_hash"] = plan_hash
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if route_deployment_id is not UNSET:
            field_dict["route_deployment_id"] = route_deployment_id
        if recovery_revision_id is not UNSET:
            field_dict["recovery_revision_id"] = recovery_revision_id
        if route_generation is not UNSET:
            field_dict["route_generation"] = route_generation
        if recovery_generation is not UNSET:
            field_dict["recovery_generation"] = recovery_generation
        if deployment_ids is not UNSET:
            field_dict["deployment_ids"] = deployment_ids
        if queue_binding_ids is not UNSET:
            field_dict["queue_binding_ids"] = queue_binding_ids
        if expected_gateways is not UNSET:
            field_dict["expected_gateways"] = expected_gateways
        if acknowledged_gateways is not UNSET:
            field_dict["acknowledged_gateways"] = acknowledged_gateways
        if retired_at is not UNSET:
            field_dict["retired_at"] = retired_at
        if recovered_at is not UNSET:
            field_dict["recovered_at"] = recovered_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        environment_id = UUID(d.pop("environment_id"))

        source_id = UUID(d.pop("source_id"))

        app_id = UUID(d.pop("app_id"))

        revision_id = UUID(d.pop("revision_id"))

        resource = d.pop("resource")

        phase = check_environment_workload_retirement_phase(d.pop("phase"))

        generation = d.pop("generation")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        _account_id = d.pop("account_id", UNSET)
        account_id: UUID | Unset
        if isinstance(_account_id, Unset):
            account_id = UNSET
        else:
            account_id = UUID(_account_id)

        _project_id = d.pop("project_id", UNSET)
        project_id: UUID | Unset
        if isinstance(_project_id, Unset):
            project_id = UNSET
        else:
            project_id = UUID(_project_id)

        plan_hash = d.pop("plan_hash", UNSET)

        job_id = d.pop("job_id", UNSET)

        route_deployment_id = d.pop("route_deployment_id", UNSET)

        recovery_revision_id = d.pop("recovery_revision_id", UNSET)

        route_generation = d.pop("route_generation", UNSET)

        recovery_generation = d.pop("recovery_generation", UNSET)

        deployment_ids = cast(list[str], d.pop("deployment_ids", UNSET))

        queue_binding_ids = cast(list[str], d.pop("queue_binding_ids", UNSET))

        expected_gateways = cast(list[str], d.pop("expected_gateways", UNSET))

        acknowledged_gateways = cast(list[str], d.pop("acknowledged_gateways", UNSET))

        def _parse_retired_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                retired_at_type_0 = datetime.datetime.fromisoformat(data)

                return retired_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        retired_at = _parse_retired_at(d.pop("retired_at", UNSET))

        def _parse_recovered_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                recovered_at_type_0 = datetime.datetime.fromisoformat(data)

                return recovered_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        recovered_at = _parse_recovered_at(d.pop("recovered_at", UNSET))

        environment_workload_retirement = cls(
            id=id,
            environment_id=environment_id,
            source_id=source_id,
            app_id=app_id,
            revision_id=revision_id,
            resource=resource,
            phase=phase,
            generation=generation,
            requested_at=requested_at,
            account_id=account_id,
            project_id=project_id,
            plan_hash=plan_hash,
            job_id=job_id,
            route_deployment_id=route_deployment_id,
            recovery_revision_id=recovery_revision_id,
            route_generation=route_generation,
            recovery_generation=recovery_generation,
            deployment_ids=deployment_ids,
            queue_binding_ids=queue_binding_ids,
            expected_gateways=expected_gateways,
            acknowledged_gateways=acknowledged_gateways,
            retired_at=retired_at,
            recovered_at=recovered_at,
        )

        environment_workload_retirement.additional_properties = d
        return environment_workload_retirement

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
