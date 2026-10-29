from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeploymentResponseCheckCapabilities")


@_attrs_define
class DeploymentResponseCheckCapabilities:
    """Installed deployment-check protocol and forwarding configuration; no workload admission is performed."""

    protocol_version: int
    """Protocol implemented by this API; clients require a compatible version."""
    available: bool
    unavailable_reason: str | Unset = UNSET
    """Stable gateway_unsupported, gateway_unconfigured, or gateway_unavailable code when unavailable."""

    def to_dict(self) -> dict[str, Any]:
        protocol_version = self.protocol_version

        available = self.available

        unavailable_reason = self.unavailable_reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "protocol_version": protocol_version,
                "available": available,
            }
        )
        if unavailable_reason is not UNSET:
            field_dict["unavailable_reason"] = unavailable_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        protocol_version = d.pop("protocol_version")

        available = d.pop("available")

        unavailable_reason = d.pop("unavailable_reason", UNSET)

        deployment_response_check_capabilities = cls(
            protocol_version=protocol_version,
            available=available,
            unavailable_reason=unavailable_reason,
        )

        return deployment_response_check_capabilities
