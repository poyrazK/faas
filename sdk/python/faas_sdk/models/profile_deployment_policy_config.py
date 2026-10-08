from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.periodic_profile_policy import PeriodicProfilePolicy
    from ..models.profile_canary_gate_policy import ProfileCanaryGatePolicy
    from ..models.profile_regression_options import ProfileRegressionOptions


T = TypeVar("T", bound="ProfileDeploymentPolicyConfig")


@_attrs_define
class ProfileDeploymentPolicyConfig:
    """Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters apply to
    both deployments; capture coverage is not instrumentation completeness.

    """

    runtime: str
    options: ProfileRegressionOptions
    """Threshold policy for sampled CPU per wall-clock second or per weighted observed request. Both relative and
    selected-metric absolute increase thresholds must be met. CPU-per-request mode also requires retained request
    telemetry and its configured minimum request count in both deployment windows; absent or sparse counts are
    inconclusive. Request telemetry rows use minute-bucket timestamps, so counts near window boundaries can be
    approximate and may be incomplete. Capture requirements apply separately to each profile window."""
    enabled: bool = False
    window_seconds: int = 300
    warmup_seconds: int = 120
    canary_gate: None | ProfileCanaryGatePolicy | Unset = UNSET
    """Omitted or null keeps canary checks advisory. Requires enabled checks and explicit routes."""
    periodic: None | PeriodicProfilePolicy | Unset = UNSET
    """Omitted or null disables periodic monitoring. Enabled automatic checks and explicit advisory routes are
    required."""
    notify_route_regressions: bool | Unset = False
    """Advisory app webhook notifications on evidence-qualified route regression and recovery transitions. Requires
    enabled checks and explicit options.routes. Applies to deployment and canary checks; does not affect rollout
    decisions."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.periodic_profile_policy import PeriodicProfilePolicy
        from ..models.profile_canary_gate_policy import ProfileCanaryGatePolicy

        enabled = self.enabled

        runtime = self.runtime

        window_seconds = self.window_seconds

        warmup_seconds = self.warmup_seconds

        options = self.options.to_dict()

        canary_gate: dict[str, Any] | None | Unset
        if isinstance(self.canary_gate, Unset):
            canary_gate = UNSET
        elif isinstance(self.canary_gate, ProfileCanaryGatePolicy):
            canary_gate = self.canary_gate.to_dict()
        else:
            canary_gate = self.canary_gate

        periodic: dict[str, Any] | None | Unset
        if isinstance(self.periodic, Unset):
            periodic = UNSET
        elif isinstance(self.periodic, PeriodicProfilePolicy):
            periodic = self.periodic.to_dict()
        else:
            periodic = self.periodic

        notify_route_regressions = self.notify_route_regressions

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "runtime": runtime,
                "window_seconds": window_seconds,
                "warmup_seconds": warmup_seconds,
                "options": options,
            }
        )
        if canary_gate is not UNSET:
            field_dict["canary_gate"] = canary_gate
        if periodic is not UNSET:
            field_dict["periodic"] = periodic
        if notify_route_regressions is not UNSET:
            field_dict["notify_route_regressions"] = notify_route_regressions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.periodic_profile_policy import PeriodicProfilePolicy
        from ..models.profile_canary_gate_policy import ProfileCanaryGatePolicy
        from ..models.profile_regression_options import ProfileRegressionOptions

        d = dict(src_dict)
        enabled = d.pop("enabled")

        runtime = d.pop("runtime")

        window_seconds = d.pop("window_seconds")

        warmup_seconds = d.pop("warmup_seconds")

        options = ProfileRegressionOptions.from_dict(d.pop("options"))

        def _parse_canary_gate(data: object) -> None | ProfileCanaryGatePolicy | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_gate_type_1 = ProfileCanaryGatePolicy.from_dict(data)

                return canary_gate_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ProfileCanaryGatePolicy | Unset, data)

        canary_gate = _parse_canary_gate(d.pop("canary_gate", UNSET))

        def _parse_periodic(data: object) -> None | PeriodicProfilePolicy | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                periodic_type_1 = PeriodicProfilePolicy.from_dict(data)

                return periodic_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | PeriodicProfilePolicy | Unset, data)

        periodic = _parse_periodic(d.pop("periodic", UNSET))

        notify_route_regressions = d.pop("notify_route_regressions", UNSET)

        profile_deployment_policy_config = cls(
            enabled=enabled,
            runtime=runtime,
            window_seconds=window_seconds,
            warmup_seconds=warmup_seconds,
            options=options,
            canary_gate=canary_gate,
            periodic=periodic,
            notify_route_regressions=notify_route_regressions,
        )

        profile_deployment_policy_config.additional_properties = d
        return profile_deployment_policy_config

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
