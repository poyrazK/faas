from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="RescheduleEnvironmentCacheRequest")


@_attrs_define
class RescheduleEnvironmentCacheRequest:
    """Pinned original execution timestamp, immutable target and exact new schedule identity/time; action is taken from the
    original record.

    """

    replacement_id: UUID
    app_id: UUID
    project_id: UUID
    environment_id: UUID
    expected_execute_at: datetime.datetime
    execute_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        replacement_id = str(self.replacement_id)

        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        expected_execute_at = self.expected_execute_at.isoformat()

        execute_at = self.execute_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "replacement_id": replacement_id,
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "expected_execute_at": expected_execute_at,
                "execute_at": execute_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        replacement_id = UUID(d.pop("replacement_id"))

        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        expected_execute_at = datetime.datetime.fromisoformat(d.pop("expected_execute_at"))

        execute_at = datetime.datetime.fromisoformat(d.pop("execute_at"))

        reschedule_environment_cache_request = cls(
            replacement_id=replacement_id,
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            expected_execute_at=expected_execute_at,
            execute_at=execute_at,
        )

        return reschedule_environment_cache_request
