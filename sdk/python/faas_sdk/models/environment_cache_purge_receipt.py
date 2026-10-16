from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentCachePurgeReceipt")


@_attrs_define
class EnvironmentCachePurgeReceipt:
    """Stable durable logical cache-purge intent for one immutable environment host."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    request_id: UUID
    path: str
    generation: int
    requested_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        request_id = str(self.request_id)

        path = self.path

        generation = self.generation

        requested_at = self.requested_at.isoformat()

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

        environment_cache_purge_receipt = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            request_id=request_id,
            path=path,
            generation=generation,
            requested_at=requested_at,
        )

        return environment_cache_purge_receipt
