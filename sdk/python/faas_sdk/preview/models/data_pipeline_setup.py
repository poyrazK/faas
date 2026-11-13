from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_setup_request import DataPipelineSetupRequest


T = TypeVar("T", bound="DataPipelineSetup")


@_attrs_define
class DataPipelineSetup:
    """Account-owned durable setup progress with public recovery IDs. Cancellation retains allocated resources for separate
    management. Private provider plans and credentials are never returned.

    """

    id: UUID
    request: DataPipelineSetupRequest
    """Immutable durable setup intent. Repeating exactly the same normalized name and selections recovers the saved
    job and resource IDs. Source discovery must already include the selected tables; the worker refreshes
    credentials and capacity before allocating resources."""
    database_id: UUID
    state: str
    phase: str
    revision: int
    progress: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    pipeline_id: UUID | Unset = UNSET
    reason: str | Unset = UNSET
    data_api_app_id: UUID | Unset = UNSET
    deployment_id: UUID | Unset = UNSET
    endpoint: str | Unset = UNSET
    region: str | Unset = UNSET
    storage_limit_bytes: int | Unset = UNSET
    recovery: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        request = self.request.to_dict()

        database_id = str(self.database_id)

        state = self.state

        phase = self.phase

        revision = self.revision

        progress = self.progress

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        pipeline_id: str | Unset = UNSET
        if not isinstance(self.pipeline_id, Unset):
            pipeline_id = str(self.pipeline_id)

        reason = self.reason

        data_api_app_id: str | Unset = UNSET
        if not isinstance(self.data_api_app_id, Unset):
            data_api_app_id = str(self.data_api_app_id)

        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        endpoint = self.endpoint

        region = self.region

        storage_limit_bytes = self.storage_limit_bytes

        recovery = self.recovery

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "request": request,
                "database_id": database_id,
                "state": state,
                "phase": phase,
                "revision": revision,
                "progress": progress,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if pipeline_id is not UNSET:
            field_dict["pipeline_id"] = pipeline_id
        if reason is not UNSET:
            field_dict["reason"] = reason
        if data_api_app_id is not UNSET:
            field_dict["data_api_app_id"] = data_api_app_id
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if endpoint is not UNSET:
            field_dict["endpoint"] = endpoint
        if region is not UNSET:
            field_dict["region"] = region
        if storage_limit_bytes is not UNSET:
            field_dict["storage_limit_bytes"] = storage_limit_bytes
        if recovery is not UNSET:
            field_dict["recovery"] = recovery

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_setup_request import DataPipelineSetupRequest

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        request = DataPipelineSetupRequest.from_dict(d.pop("request"))

        database_id = UUID(d.pop("database_id"))

        state = d.pop("state")

        phase = d.pop("phase")

        revision = d.pop("revision")

        progress = d.pop("progress")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _pipeline_id = d.pop("pipeline_id", UNSET)
        pipeline_id: UUID | Unset
        if isinstance(_pipeline_id, Unset):
            pipeline_id = UNSET
        else:
            pipeline_id = UUID(_pipeline_id)

        reason = d.pop("reason", UNSET)

        _data_api_app_id = d.pop("data_api_app_id", UNSET)
        data_api_app_id: UUID | Unset
        if isinstance(_data_api_app_id, Unset):
            data_api_app_id = UNSET
        else:
            data_api_app_id = UUID(_data_api_app_id)

        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        endpoint = d.pop("endpoint", UNSET)

        region = d.pop("region", UNSET)

        storage_limit_bytes = d.pop("storage_limit_bytes", UNSET)

        recovery = d.pop("recovery", UNSET)

        data_pipeline_setup = cls(
            id=id,
            request=request,
            database_id=database_id,
            state=state,
            phase=phase,
            revision=revision,
            progress=progress,
            created_at=created_at,
            updated_at=updated_at,
            pipeline_id=pipeline_id,
            reason=reason,
            data_api_app_id=data_api_app_id,
            deployment_id=deployment_id,
            endpoint=endpoint,
            region=region,
            storage_limit_bytes=storage_limit_bytes,
            recovery=recovery,
        )

        return data_pipeline_setup
