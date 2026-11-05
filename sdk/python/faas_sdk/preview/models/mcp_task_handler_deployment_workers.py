from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPTaskHandlerDeploymentWorkers")


@_attrs_define
class MCPTaskHandlerDeploymentWorkers:
    """Compatible workers attributed to a specific serving deployment."""

    deployment_id: UUID
    workers: int

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        workers = self.workers

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "deployment_id": deployment_id,
                "workers": workers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        workers = d.pop("workers")

        mcp_task_handler_deployment_workers = cls(
            deployment_id=deployment_id,
            workers=workers,
        )

        return mcp_task_handler_deployment_workers
