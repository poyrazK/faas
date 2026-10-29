from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.data_api_runtime_connection_report_runtime_model import (
    DataAPIRuntimeConnectionReportRuntimeModel,
    check_data_api_runtime_connection_report_runtime_model,
)
from ..models.data_api_runtime_connection_report_state import (
    DataAPIRuntimeConnectionReportState,
    check_data_api_runtime_connection_report_state,
)
from ..models.data_api_runtime_connection_report_version import (
    DataAPIRuntimeConnectionReportVersion,
    check_data_api_runtime_connection_report_version,
)

T = TypeVar("T", bound="DataAPIRuntimeConnectionReport")


@_attrs_define
class DataAPIRuntimeConnectionReport:
    """Private versioned runtime configuration report. Instance, deployment, node and database placement authority are
    resolved from workload identity and the catalog, never accepted from this body. observed_at bounds replay; unknown
    invalidates the previous observation.

    """

    version: DataAPIRuntimeConnectionReportVersion
    binding_id: UUID
    database_identity: str
    """SHA-256 of backend_fingerprint, a colon and the frozen data_resource_id, from operator configuration."""
    database_generation: int
    credential_generation: int
    role: str
    state: DataAPIRuntimeConnectionReportState
    runtime_model: DataAPIRuntimeConnectionReportRuntimeModel
    engine_connections: int
    refresh_connections: int
    observed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        binding_id = str(self.binding_id)

        database_identity = self.database_identity

        database_generation = self.database_generation

        credential_generation = self.credential_generation

        role = self.role

        state: str = self.state

        runtime_model: str = self.runtime_model

        engine_connections = self.engine_connections

        refresh_connections = self.refresh_connections

        observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "binding_id": binding_id,
                "database_identity": database_identity,
                "database_generation": database_generation,
                "credential_generation": credential_generation,
                "role": role,
                "state": state,
                "runtime_model": runtime_model,
                "engine_connections": engine_connections,
                "refresh_connections": refresh_connections,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = check_data_api_runtime_connection_report_version(d.pop("version"))

        binding_id = UUID(d.pop("binding_id"))

        database_identity = d.pop("database_identity")

        database_generation = d.pop("database_generation")

        credential_generation = d.pop("credential_generation")

        role = d.pop("role")

        state = check_data_api_runtime_connection_report_state(d.pop("state"))

        runtime_model = check_data_api_runtime_connection_report_runtime_model(d.pop("runtime_model"))

        engine_connections = d.pop("engine_connections")

        refresh_connections = d.pop("refresh_connections")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        data_api_runtime_connection_report = cls(
            version=version,
            binding_id=binding_id,
            database_identity=database_identity,
            database_generation=database_generation,
            credential_generation=credential_generation,
            role=role,
            state=state,
            runtime_model=runtime_model,
            engine_connections=engine_connections,
            refresh_connections=refresh_connections,
            observed_at=observed_at,
        )

        return data_api_runtime_connection_report
