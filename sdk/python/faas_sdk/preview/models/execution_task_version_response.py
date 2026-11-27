from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_task_version_response_artifact_storage import (
    ExecutionTaskVersionResponseArtifactStorage,
    check_execution_task_version_response_artifact_storage,
)
from ..models.execution_task_version_response_status import (
    ExecutionTaskVersionResponseStatus,
    check_execution_task_version_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.resolved_execution_limits import ResolvedExecutionLimits


T = TypeVar("T", bound="ExecutionTaskVersionResponse")


@_attrs_define
class ExecutionTaskVersionResponse:
    """Published task configuration, lifecycle status and version metadata."""

    id: UUID
    name: str
    version: int
    digest: str
    runtime: str
    profile: str
    limits: ResolvedExecutionLimits
    """Immutable limits admitted and enforced for one execution."""
    status: ExecutionTaskVersionResponseStatus
    created_at: datetime.datetime
    environment_id: UUID | Unset = UNSET
    input_schema: Any | Unset = UNSET
    result_schema: Any | Unset = UNSET
    integration_ids: list[str] | Unset = UNSET
    output_files: list[str] | Unset = UNSET
    artifact_storage: ExecutionTaskVersionResponseArtifactStorage | Unset = UNSET
    agent_endpoint_ids: list[UUID] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        version = self.version

        digest = self.digest

        runtime = self.runtime

        profile = self.profile

        limits = self.limits.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        input_schema = self.input_schema

        result_schema = self.result_schema

        integration_ids: list[str] | Unset = UNSET
        if not isinstance(self.integration_ids, Unset):
            integration_ids = self.integration_ids

        output_files: list[str] | Unset = UNSET
        if not isinstance(self.output_files, Unset):
            output_files = self.output_files

        artifact_storage: str | Unset = UNSET
        if not isinstance(self.artifact_storage, Unset):
            artifact_storage = self.artifact_storage

        agent_endpoint_ids: list[str] | Unset = UNSET
        if not isinstance(self.agent_endpoint_ids, Unset):
            agent_endpoint_ids = []
            for agent_endpoint_ids_item_data in self.agent_endpoint_ids:
                agent_endpoint_ids_item = str(agent_endpoint_ids_item_data)
                agent_endpoint_ids.append(agent_endpoint_ids_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "version": version,
                "digest": digest,
                "runtime": runtime,
                "profile": profile,
                "limits": limits,
                "status": status,
                "created_at": created_at,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if input_schema is not UNSET:
            field_dict["input_schema"] = input_schema
        if result_schema is not UNSET:
            field_dict["result_schema"] = result_schema
        if integration_ids is not UNSET:
            field_dict["integration_ids"] = integration_ids
        if output_files is not UNSET:
            field_dict["output_files"] = output_files
        if artifact_storage is not UNSET:
            field_dict["artifact_storage"] = artifact_storage
        if agent_endpoint_ids is not UNSET:
            field_dict["agent_endpoint_ids"] = agent_endpoint_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.resolved_execution_limits import ResolvedExecutionLimits

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        version = d.pop("version")

        digest = d.pop("digest")

        runtime = d.pop("runtime")

        profile = d.pop("profile")

        limits = ResolvedExecutionLimits.from_dict(d.pop("limits"))

        status = check_execution_task_version_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        input_schema = d.pop("input_schema", UNSET)

        result_schema = d.pop("result_schema", UNSET)

        integration_ids = cast(list[str], d.pop("integration_ids", UNSET))

        output_files = cast(list[str], d.pop("output_files", UNSET))

        _artifact_storage = d.pop("artifact_storage", UNSET)
        artifact_storage: ExecutionTaskVersionResponseArtifactStorage | Unset
        if isinstance(_artifact_storage, Unset):
            artifact_storage = UNSET
        else:
            artifact_storage = check_execution_task_version_response_artifact_storage(_artifact_storage)

        _agent_endpoint_ids = d.pop("agent_endpoint_ids", UNSET)
        agent_endpoint_ids: list[UUID] | Unset = UNSET
        if _agent_endpoint_ids is not UNSET:
            agent_endpoint_ids = []
            for agent_endpoint_ids_item_data in _agent_endpoint_ids:
                agent_endpoint_ids_item = UUID(agent_endpoint_ids_item_data)

                agent_endpoint_ids.append(agent_endpoint_ids_item)

        execution_task_version_response = cls(
            id=id,
            name=name,
            version=version,
            digest=digest,
            runtime=runtime,
            profile=profile,
            limits=limits,
            status=status,
            created_at=created_at,
            environment_id=environment_id,
            input_schema=input_schema,
            result_schema=result_schema,
            integration_ids=integration_ids,
            output_files=output_files,
            artifact_storage=artifact_storage,
            agent_endpoint_ids=agent_endpoint_ids,
        )

        execution_task_version_response.additional_properties = d
        return execution_task_version_response

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
