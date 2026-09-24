from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_grpc_healthcheck import DeploymentGRPCHealthcheck


T = TypeVar("T", bound="DeploymentHealthcheck")


@_attrs_define
class DeploymentHealthcheck:
    """Readiness-probe shape on the deploy-time override object (issue #460 /
    ADR-053). Exactly one of `path` (HTTP) or `grpc` (standard gRPC health
    Check) selects the readiness action. The gRPC probe uses the app's
    published port; an empty service checks overall server health.

    Validation rules (enforced in `pkg/api/dto.go::CreateDeploymentOverrides.Validate`):
    - Exactly one of `path` and `grpc` must be set.
    - `path`, when set, must start with `/`.
    - `grpc.service` is optional and limited to 256 characters.
    - `interval_s`, `timeout_s`, `retries` must be `>= 0`.
    - Missing tuning fields default to 0; the host readiness deadline is
      resolved separately from the app's plan and startup policy.

    OCI `test` argv and `start_period_s` remain deploy metadata; the host
    readiness gate uses only the selected HTTP path or gRPC health RPC.

    """

    path: str | Unset = UNSET
    """HTTP readiness path requested from the guest; must start with `/` (e.g. `/healthz`). Set exactly one of path
    or grpc."""
    grpc: DeploymentGRPCHealthcheck | Unset = UNSET
    """Standard gRPC health.v1 readiness probe. An omitted or empty service checks overall server health."""
    interval_s: int | Unset = UNSET
    """Probe interval in seconds; 0 = use image default."""
    timeout_s: int | Unset = UNSET
    """Probe timeout in seconds; 0 = use image default."""
    retries: int | Unset = UNSET
    """Consecutive failures before the instance is considered unhealthy; 0 = use image default."""
    test: list[str] | Unset = UNSET
    """Argv of the OCI HEALTHCHECK command, prefixed by "CMD", "CMD-SHELL", or "NONE". Surfaces onto
    AppManifest.Healthcheck.Test at apply_overrides time."""
    start_period_s: int | Unset = UNSET
    """Startup grace during which probe failures don't count (Docker 17.05+, default 0s). Surfaces onto
    AppManifest.Healthcheck.StartPeriodS."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        grpc: dict[str, Any] | Unset = UNSET
        if not isinstance(self.grpc, Unset):
            grpc = self.grpc.to_dict()

        interval_s = self.interval_s

        timeout_s = self.timeout_s

        retries = self.retries

        test: list[str] | Unset = UNSET
        if not isinstance(self.test, Unset):
            test = self.test

        start_period_s = self.start_period_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if path is not UNSET:
            field_dict["path"] = path
        if grpc is not UNSET:
            field_dict["grpc"] = grpc
        if interval_s is not UNSET:
            field_dict["interval_s"] = interval_s
        if timeout_s is not UNSET:
            field_dict["timeout_s"] = timeout_s
        if retries is not UNSET:
            field_dict["retries"] = retries
        if test is not UNSET:
            field_dict["test"] = test
        if start_period_s is not UNSET:
            field_dict["start_period_s"] = start_period_s

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

        interval_s = d.pop("interval_s", UNSET)

        timeout_s = d.pop("timeout_s", UNSET)

        retries = d.pop("retries", UNSET)

        test = cast(list[str], d.pop("test", UNSET))

        start_period_s = d.pop("start_period_s", UNSET)

        deployment_healthcheck = cls(
            path=path,
            grpc=grpc,
            interval_s=interval_s,
            timeout_s=timeout_s,
            retries=retries,
            test=test,
            start_period_s=start_period_s,
        )

        deployment_healthcheck.additional_properties = d
        return deployment_healthcheck

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
