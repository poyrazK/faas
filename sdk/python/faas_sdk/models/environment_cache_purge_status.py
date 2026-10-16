from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.environment_cache_purge_status_status import (
    EnvironmentCachePurgeStatusStatus,
    check_environment_cache_purge_status_status,
)

T = TypeVar("T", bound="EnvironmentCachePurgeStatus")


@_attrs_define
class EnvironmentCachePurgeStatus:
    """Purge intent plus fresh current-process replay evidence for the registered compute ingress fleet."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    request_id: UUID
    path: str
    generation: int
    requested_at: datetime.datetime
    status: EnvironmentCachePurgeStatusStatus
    checked_at: datetime.datetime
    serving_gateways: int
    acknowledged_gateways: int
    pending_gateways: int
    stale_gateways: int

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        request_id = str(self.request_id)

        path = self.path

        generation = self.generation

        requested_at = self.requested_at.isoformat()

        status: str = self.status

        checked_at = self.checked_at.isoformat()

        serving_gateways = self.serving_gateways

        acknowledged_gateways = self.acknowledged_gateways

        pending_gateways = self.pending_gateways

        stale_gateways = self.stale_gateways

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "request_id": request_id,
                "path": path,
                "generation": generation,
                "requested_at": requested_at,
                "status": status,
                "checked_at": checked_at,
                "serving_gateways": serving_gateways,
                "acknowledged_gateways": acknowledged_gateways,
                "pending_gateways": pending_gateways,
                "stale_gateways": stale_gateways,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        scope = d.pop("scope")

        request_id = UUID(d.pop("request_id"))

        path = d.pop("path")

        generation = d.pop("generation")

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        status = check_environment_cache_purge_status_status(d.pop("status"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        serving_gateways = d.pop("serving_gateways")

        acknowledged_gateways = d.pop("acknowledged_gateways")

        pending_gateways = d.pop("pending_gateways")

        stale_gateways = d.pop("stale_gateways")

        environment_cache_purge_status = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            request_id=request_id,
            path=path,
            generation=generation,
            requested_at=requested_at,
            status=status,
            checked_at=checked_at,
            serving_gateways=serving_gateways,
            acknowledged_gateways=acknowledged_gateways,
            pending_gateways=pending_gateways,
            stale_gateways=stale_gateways,
        )

        return environment_cache_purge_status
