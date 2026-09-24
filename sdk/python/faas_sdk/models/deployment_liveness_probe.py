from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_grpc_liveness_probe import DeploymentGRPCLivenessProbe


T = TypeVar("T", bound="DeploymentLivenessProbe")


@_attrs_define
class DeploymentLivenessProbe:
    """Liveness-probe shape on the deploy-time override object (issue #554 / ADR-078).
    The probe is the Cloud-Run-parity primitive that asks "is the VM still
    responding?" — a wedged guest (busy-loop, leaked fd, deadlocked runner)
    sits resident billing RAM-hours while serving 5xx until the §13 idle
    reaper eventually fires. The liveness probe replaces that wait with the
    short, customer-visible path: 3 consecutive failures → destroy →
    cold-boot from rootfs (per ADR-005 — never snapshot-restore).

    Distinct from `DeploymentHealthcheck`: readiness is "accept traffic?",
    liveness is "still alive?". A passing readiness but failing liveness
    is the canonical "wake succeeded, then the runtime died" failure mode
    that this primitive is designed to catch.

    Validation rules (enforced in `pkg/api/dto.go::CreateDeploymentOverrides.Validate`):
    - Exactly one of `path` (HTTP) or `grpc` (standard gRPC health Check) must be set.
      The server enforces this mutual-exclusion rule.
    - `path`, when set, must start with `/`.
    - `grpc.service` is optional and limited to 256 characters.
    - `interval_s` ∈ [0, 60] (0 = inherit per-plan default; Hobby/Pro/Scale → 5 s).
    - `timeout_s` ∈ [0, 5] (0 = inherit 2 s) for either probe action.
    - `consecutive_failures` ∈ [0, 10] (0 = inherit per-plan default; Hobby/Pro/Scale → 3).
    - `cooldown_s` ∈ [10, 600] (cooldown gate enforced by the vmmd-side
      probe loop after a destroy fires — see ADR-078; 0 = no cooldown, the
      legacy Free-plan behaviour).

    Per-deployment overrides of `interval_s` / `timeout_s` / `consecutive_failures` / `cooldown_s`
    are clamped to the bounds above. The counter resets to 0 on the first 2xx
    and survives an intermittent 5xx across the consecutive window (AC #2:
    flaky app does NOT oscillate). The cooldown gate short-circuits the
    counter when a probe fires inside the previous destroy's window so the
    cold-boot replacement instance has a grace period to settle.

    """

    path: str | Unset = UNSET
    """HTTP path the probe requests from the guest; must start with `/` (e.g. `/healthz`). Reuses the runner's
    existing listener — no runner changes (issue #554 §4). Set exactly one of path or grpc."""
    grpc: DeploymentGRPCLivenessProbe | Unset = UNSET
    """Standard gRPC health.v1 liveness probe. An omitted or empty service checks overall server health."""
    interval_s: int | Unset = UNSET
    """Per-plan poll cadence in seconds; 0 = inherit per-plan default (Hobby/Pro/Scale → 5 s). Clamped to
    [MinLivenessPeriodSeconds=1, MaxLivenessPeriodSeconds=60]."""
    timeout_s: int | Unset = UNSET
    """Per-probe HTTP or gRPC timeout in seconds; 0 = inherit 2 s default. A timeout is treated identically to a
    non-2xx response by the failure counter."""
    consecutive_failures: int | Unset = UNSET
    """N at which DestroyForLivenessFailure fires; 0 = inherit per-plan default (Hobby/Pro/Scale → 3). The counter
    is reset to 0 on the first 2xx and survives an intermittent 5xx across the consecutive window (AC #2 — flaky app
    does NOT oscillate)."""
    cooldown_s: int | Unset = UNSET
    """Cooldown window in seconds after a liveness-driven destroy fires. While inside the window the vmmd-side
    probe loop short-circuits the failure counter so the cold-boot replacement has time to settle (issue #554
    closure / ADR-078). 0 = no cooldown (Free-plan / legacy behaviour); clamped to [MinLivenessCooldownSeconds=10,
    MaxLivenessCooldownSeconds=600] when the field is populated by a Pro/Scale deployment override."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        grpc: dict[str, Any] | Unset = UNSET
        if not isinstance(self.grpc, Unset):
            grpc = self.grpc.to_dict()

        interval_s = self.interval_s

        timeout_s = self.timeout_s

        consecutive_failures = self.consecutive_failures

        cooldown_s = self.cooldown_s

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
        if consecutive_failures is not UNSET:
            field_dict["consecutive_failures"] = consecutive_failures
        if cooldown_s is not UNSET:
            field_dict["cooldown_s"] = cooldown_s

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_grpc_liveness_probe import DeploymentGRPCLivenessProbe

        d = dict(src_dict)
        path = d.pop("path", UNSET)

        _grpc = d.pop("grpc", UNSET)
        grpc: DeploymentGRPCLivenessProbe | Unset
        if isinstance(_grpc, Unset):
            grpc = UNSET
        else:
            grpc = DeploymentGRPCLivenessProbe.from_dict(_grpc)

        interval_s = d.pop("interval_s", UNSET)

        timeout_s = d.pop("timeout_s", UNSET)

        consecutive_failures = d.pop("consecutive_failures", UNSET)

        cooldown_s = d.pop("cooldown_s", UNSET)

        deployment_liveness_probe = cls(
            path=path,
            grpc=grpc,
            interval_s=interval_s,
            timeout_s=timeout_s,
            consecutive_failures=consecutive_failures,
            cooldown_s=cooldown_s,
        )

        deployment_liveness_probe.additional_properties = d
        return deployment_liveness_probe

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
