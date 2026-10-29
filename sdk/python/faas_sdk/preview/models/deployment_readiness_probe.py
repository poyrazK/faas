from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_grpc_healthcheck import DeploymentGRPCHealthcheck


T = TypeVar("T", bound="DeploymentReadinessProbe")


@_attrs_define
class DeploymentReadinessProbe:
    """Continuous primary-app readiness policy. It is evaluated only after
    startup readiness succeeds. Once `failure_threshold` consecutive checks
    fail, the running instance is removed from request routing. A successful
    check restores routing; readiness failures never restart the VM.

    Defaults are period 5 seconds, timeout 2 seconds, and failure threshold
    3. Zero means use the corresponding default. Exactly one of `path`
    (HTTP) and `grpc` (standard gRPC health.v1 Check) is required.

    """

    path: str | Unset = UNSET
    """HTTP path requested on the app's runtime port; must start with `/`. Set exactly one of path or grpc."""
    grpc: DeploymentGRPCHealthcheck | Unset = UNSET
    """Standard gRPC health.v1 readiness probe. An omitted or empty service checks overall server health."""
    period_s: int | Unset = UNSET
    """Probe interval in seconds; 0 = default (5)."""
    timeout_s: int | Unset = UNSET
    """Per-probe timeout in seconds; 0 = default (2)."""
    failure_threshold: int | Unset = UNSET
    """Consecutive failures before traffic is withdrawn; 0 = default (3). A passing probe immediately restores
    routing."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        grpc: dict[str, Any] | Unset = UNSET
        if not isinstance(self.grpc, Unset):
            grpc = self.grpc.to_dict()

        period_s = self.period_s

        timeout_s = self.timeout_s

        failure_threshold = self.failure_threshold

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if path is not UNSET:
            field_dict["path"] = path
        if grpc is not UNSET:
            field_dict["grpc"] = grpc
        if period_s is not UNSET:
            field_dict["period_s"] = period_s
        if timeout_s is not UNSET:
            field_dict["timeout_s"] = timeout_s
        if failure_threshold is not UNSET:
            field_dict["failure_threshold"] = failure_threshold

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_grpc_healthcheck import DeploymentGRPCHealthcheck

        d = dict(src_dict)
        path = d.pop("path", UNSET)

        _grpc = d.pop("grpc", UNSET)
        grpc: DeploymentGRPCHealthcheck | Unset
        if isinstance(_grpc, Unset):
            grpc = UNSET
        else:
            grpc = DeploymentGRPCHealthcheck.from_dict(_grpc)

        period_s = d.pop("period_s", UNSET)

        timeout_s = d.pop("timeout_s", UNSET)

        failure_threshold = d.pop("failure_threshold", UNSET)

        deployment_readiness_probe = cls(
            path=path,
            grpc=grpc,
            period_s=period_s,
            timeout_s=timeout_s,
            failure_threshold=failure_threshold,
        )

        deployment_readiness_probe.additional_properties = d
        return deployment_readiness_probe

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
