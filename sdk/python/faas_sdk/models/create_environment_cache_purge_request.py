from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateEnvironmentCachePurgeRequest")


@_attrs_define
class CreateEnvironmentCachePurgeRequest:
    """Immutable named-environment purge target and stable retry identity; path must be explicit even for whole-environment
    purging.

    """

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    request_id: UUID
    """Keep this purge operation UUID and body on retries."""
    path: str
    """Exact decoded URL path; empty string selects the whole immutable environment host. Globs, query strings,
    controls, escapes and dot segments are invalid."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        request_id = str(self.request_id)

        path = self.path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "request_id": request_id,
                "path": path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        request_id = UUID(d.pop("request_id"))

        path = d.pop("path")

        create_environment_cache_purge_request = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            request_id=request_id,
            path=path,
        )

        return create_environment_cache_purge_request
