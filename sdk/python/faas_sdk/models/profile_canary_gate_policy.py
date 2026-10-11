from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.profile_canary_gate_policy_on_timeout import (
    ProfileCanaryGatePolicyOnTimeout,
    check_profile_canary_gate_policy_on_timeout,
)

T = TypeVar("T", bound="ProfileCanaryGatePolicy")


@_attrs_define
class ProfileCanaryGatePolicy:
    """Opt-in policy requiring consecutive qualified route CPU/request comparisons before a canary stage advances."""

    confirmations: int = 2
    timeout_seconds: int = 1800
    """Must cover warmup plus all confirmation windows and ingestion grace."""
    on_timeout: ProfileCanaryGatePolicyOnTimeout = "hold"
    """Continuing is an explicit acceptance of inconclusive profile evidence. Confirmed regressions remain held."""
    auto_rollback: bool = False
    """Worker-only opt-in recovery for request-mode canaries to the exact current stable predecessor. Service
    recovery uses the checked handoff flow."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        confirmations = self.confirmations

        timeout_seconds = self.timeout_seconds

        on_timeout: str = self.on_timeout

        auto_rollback = self.auto_rollback

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "confirmations": confirmations,
                "timeout_seconds": timeout_seconds,
                "on_timeout": on_timeout,
                "auto_rollback": auto_rollback,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        confirmations = d.pop("confirmations")

        timeout_seconds = d.pop("timeout_seconds")

        on_timeout = check_profile_canary_gate_policy_on_timeout(d.pop("on_timeout"))

        auto_rollback = d.pop("auto_rollback")

        profile_canary_gate_policy = cls(
            confirmations=confirmations,
            timeout_seconds=timeout_seconds,
            on_timeout=on_timeout,
            auto_rollback=auto_rollback,
        )

        profile_canary_gate_policy.additional_properties = d
        return profile_canary_gate_policy

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
