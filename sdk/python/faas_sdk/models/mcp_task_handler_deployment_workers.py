from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MCPTaskHandlerDeploymentWorkers")


@_attrs_define
class MCPTaskHandlerDeploymentWorkers:
    deployment_id: str
    workers: int

    def to_dict(self) -> dict[str, Any]:
        return {"deployment_id": self.deployment_id, "workers": self.workers}

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        return cls(deployment_id=d.pop("deployment_id"), workers=d.pop("workers"))
