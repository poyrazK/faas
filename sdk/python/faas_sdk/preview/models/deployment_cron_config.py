from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="DeploymentCronConfig")


@_attrs_define
class DeploymentCronConfig:
    """Closed scheduling intent for one HTTP cron; command and execution data are excluded."""

    id: UUID
    schedule: str
    path: str
    enabled: bool
    timezone: str
    skip_if_running: bool

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        schedule = self.schedule

        path = self.path

        enabled = self.enabled

        timezone = self.timezone

        skip_if_running = self.skip_if_running

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "schedule": schedule,
                "path": path,
                "enabled": enabled,
                "timezone": timezone,
                "skip_if_running": skip_if_running,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        schedule = d.pop("schedule")

        path = d.pop("path")

        enabled = d.pop("enabled")

        timezone = d.pop("timezone")

        skip_if_running = d.pop("skip_if_running")

        deployment_cron_config = cls(
            id=id,
            schedule=schedule,
            path=path,
            enabled=enabled,
            timezone=timezone,
            skip_if_running=skip_if_running,
        )

        return deployment_cron_config
