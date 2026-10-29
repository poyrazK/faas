from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.execution_network_policy_mode import ExecutionNetworkPolicyMode, check_execution_network_policy_mode

T = TypeVar("T", bound="ExecutionNetworkPolicy")


@_attrs_define
class ExecutionNetworkPolicy:
    """Network policy for a disposable run; v1 is loopback-only."""

    mode: ExecutionNetworkPolicyMode
    """Loopback only; no tenant bridge, DNS, or public egress."""

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "mode": mode,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_execution_network_policy_mode(d.pop("mode"))

        execution_network_policy = cls(
            mode=mode,
        )

        return execution_network_policy
