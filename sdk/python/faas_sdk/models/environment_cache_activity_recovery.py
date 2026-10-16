from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentCacheActivityRecovery")


@_attrs_define
class EnvironmentCacheActivityRecovery:
    """Scoped feed checkpoint and recovery metadata for cursor_expired. Save restart_cursor before rebuilding a live
    schedule baseline, then consume events after it. Removed historical events are not recoverable; reconcile overlap by
    schedule identity.

    """

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    oldest_cursor: str
    """Last pruned sequence. Events strictly after it are retained as of this response."""
    restart_cursor: str
    """Observed head before baseline discovery; consume activity after this checkpoint to cover concurrent changes."""
    retention_seconds: int

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        oldest_cursor = self.oldest_cursor

        restart_cursor = self.restart_cursor

        retention_seconds = self.retention_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "oldest_cursor": oldest_cursor,
                "restart_cursor": restart_cursor,
                "retention_seconds": retention_seconds,
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

        oldest_cursor = d.pop("oldest_cursor")

        restart_cursor = d.pop("restart_cursor")

        retention_seconds = d.pop("retention_seconds")

        environment_cache_activity_recovery = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            oldest_cursor=oldest_cursor,
            restart_cursor=restart_cursor,
            retention_seconds=retention_seconds,
        )

        return environment_cache_activity_recovery
