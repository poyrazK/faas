from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_execution_request_runtime import (
    CreateExecutionRequestRuntime,
    check_create_execution_request_runtime,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_limit_request import ExecutionLimitRequest
    from ..models.execution_network_policy import ExecutionNetworkPolicy


T = TypeVar("T", bound="CreateExecutionRequest")


@_attrs_define
class CreateExecutionRequest:
    """Source and JSON input for one disposable execution. v1 supports only
    the listed interpreter runtimes and `network.mode=none`; dependencies,
    secrets, environment injection, and persistent disks are not part of
    this contract.

    """

    runtime: CreateExecutionRequestRuntime
    source: str
    """Single-file source code; never returned by execution reads."""
    input_: Any | Unset = UNSET
    """One complete JSON value delivered to the guest as input."""
    limits: ExecutionLimitRequest | Unset = UNSET
    """Caller-selected execution resource limits; zero selects the plan default."""
    network: ExecutionNetworkPolicy | Unset = UNSET
    """Network policy for a disposable run; v1 is loopback-only."""

    def to_dict(self) -> dict[str, Any]:
        runtime: str = self.runtime

        source = self.source

        input_ = self.input_

        limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.limits, Unset):
            limits = self.limits.to_dict()

        network: dict[str, Any] | Unset = UNSET
        if not isinstance(self.network, Unset):
            network = self.network.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "runtime": runtime,
                "source": source,
            }
        )
        if input_ is not UNSET:
            field_dict["input"] = input_
        if limits is not UNSET:
            field_dict["limits"] = limits
        if network is not UNSET:
            field_dict["network"] = network

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_limit_request import ExecutionLimitRequest
        from ..models.execution_network_policy import ExecutionNetworkPolicy

        d = dict(src_dict)
        runtime = check_create_execution_request_runtime(d.pop("runtime"))

        source = d.pop("source")

        input_ = d.pop("input", UNSET)

        _limits = d.pop("limits", UNSET)
        limits: ExecutionLimitRequest | Unset
        if isinstance(_limits, Unset):
            limits = UNSET
        else:
            limits = ExecutionLimitRequest.from_dict(_limits)

        _network = d.pop("network", UNSET)
        network: ExecutionNetworkPolicy | Unset
        if isinstance(_network, Unset):
            network = UNSET
        else:
            network = ExecutionNetworkPolicy.from_dict(_network)

        create_execution_request = cls(
            runtime=runtime,
            source=source,
            input_=input_,
            limits=limits,
            network=network,
        )

        return create_execution_request
