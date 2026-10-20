from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_task_handler_deployment_workers import MCPTaskHandlerDeploymentWorkers


T = TypeVar("T", bound="MCPTaskHandlerCompatibility")


@_attrs_define
class MCPTaskHandlerCompatibility:
    """Outstanding task counts and live worker coverage for one tool and handler version."""

    tool_name: str
    handler_version: str
    waiting_tasks: int
    live_compatible_workers: int
    unknown_deployment_workers: int | Unset = UNSET
    """Workers whose deployment identity is not available yet, including workers from older starter versions."""
    deployments: list[MCPTaskHandlerDeploymentWorkers] | Unset = UNSET
    """Per-deployment worker counts. A report contains at most 768 deployment entries overall; larger snapshots set
    truncated=true."""

    def to_dict(self) -> dict[str, Any]:
        tool_name = self.tool_name

        handler_version = self.handler_version

        waiting_tasks = self.waiting_tasks

        live_compatible_workers = self.live_compatible_workers

        unknown_deployment_workers = self.unknown_deployment_workers

        deployments: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.deployments, Unset):
            deployments = []
            for deployments_item_data in self.deployments:
                deployments_item = deployments_item_data.to_dict()
                deployments.append(deployments_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "tool_name": tool_name,
                "handler_version": handler_version,
                "waiting_tasks": waiting_tasks,
                "live_compatible_workers": live_compatible_workers,
            }
        )
        if unknown_deployment_workers is not UNSET:
            field_dict["unknown_deployment_workers"] = unknown_deployment_workers
        if deployments is not UNSET:
            field_dict["deployments"] = deployments

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_task_handler_deployment_workers import MCPTaskHandlerDeploymentWorkers

        d = dict(src_dict)
        tool_name = d.pop("tool_name")

        handler_version = d.pop("handler_version")

        waiting_tasks = d.pop("waiting_tasks")

        live_compatible_workers = d.pop("live_compatible_workers")

        unknown_deployment_workers = d.pop("unknown_deployment_workers", UNSET)

        _deployments = d.pop("deployments", UNSET)
        deployments: list[MCPTaskHandlerDeploymentWorkers] | Unset = UNSET
        if _deployments is not UNSET:
            deployments = []
            for deployments_item_data in _deployments:
                deployments_item = MCPTaskHandlerDeploymentWorkers.from_dict(deployments_item_data)

                deployments.append(deployments_item)

        mcp_task_handler_compatibility = cls(
            tool_name=tool_name,
            handler_version=handler_version,
            waiting_tasks=waiting_tasks,
            live_compatible_workers=live_compatible_workers,
            unknown_deployment_workers=unknown_deployment_workers,
            deployments=deployments,
        )

        return mcp_task_handler_compatibility
