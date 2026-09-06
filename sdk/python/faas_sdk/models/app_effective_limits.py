from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppEffectiveLimits")


@_attrs_define
class AppEffectiveLimits:
    """The resource, scaling, rate, and timeout envelope currently applied to an app. Values are resolved from the app
    configuration and current plan; they describe enforcement rather than guest hardware alone.

    """

    memory_limit_mb: int
    """Memory limit configured for this app instance, in MB."""
    plan_memory_max_mb: int
    """Largest memory limit the current plan permits for an app instance, in MB."""
    ephemeral_disk_max_mb: int
    """Maximum writable ephemeral app-disk capacity for this app, in MB. This is the same physical drive1 cap
    historically named app_layer_max_mb."""
    guest_vcpus: int
    """Number of processors visible inside the guest. This is distinct from the sustained CPU cgroup limit."""
    cpu_limit_millicores: int
    """Sustained per-instance CPU allowance derived from cpu.max, expressed in millicores."""
    plan_cpu_max_millicores: int
    """Largest sustained CPU allowance the current plan permits, in millicores."""
    cpu_weight: int
    """Relative cgroup CPU scheduling weight applied when the host is contended."""
    max_instances: int
    """Effective per-app live-instance ceiling after applying the scaling policy."""
    concurrency_per_instance: int
    """Maximum in-flight requests accepted by one instance. Handler-level concurrency remains the application's
    responsibility."""
    app_request_rate_rps: int
    """Per-app edge token-bucket refill rate, in requests per second."""
    app_request_burst: int
    """Per-app edge token-bucket burst capacity."""
    account_request_rate_rpm: int
    """Account-wide edge token-bucket refill rate across all apps, in requests per minute."""
    request_budget_ms: int
    """Default end-to-end request budget before a route override, in milliseconds."""
    request_budget_max_ms: int
    """Maximum end-to-end request budget allowed through route overrides, in milliseconds."""
    response_write_timeout_s: int
    """Maximum response write window for the plan, in seconds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        memory_limit_mb = self.memory_limit_mb

        plan_memory_max_mb = self.plan_memory_max_mb

        ephemeral_disk_max_mb = self.ephemeral_disk_max_mb

        guest_vcpus = self.guest_vcpus

        cpu_limit_millicores = self.cpu_limit_millicores

        plan_cpu_max_millicores = self.plan_cpu_max_millicores

        cpu_weight = self.cpu_weight

        max_instances = self.max_instances

        concurrency_per_instance = self.concurrency_per_instance

        app_request_rate_rps = self.app_request_rate_rps

        app_request_burst = self.app_request_burst

        account_request_rate_rpm = self.account_request_rate_rpm

        request_budget_ms = self.request_budget_ms

        request_budget_max_ms = self.request_budget_max_ms

        response_write_timeout_s = self.response_write_timeout_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "memory_limit_mb": memory_limit_mb,
                "plan_memory_max_mb": plan_memory_max_mb,
                "ephemeral_disk_max_mb": ephemeral_disk_max_mb,
                "guest_vcpus": guest_vcpus,
                "cpu_limit_millicores": cpu_limit_millicores,
                "plan_cpu_max_millicores": plan_cpu_max_millicores,
                "cpu_weight": cpu_weight,
                "max_instances": max_instances,
                "concurrency_per_instance": concurrency_per_instance,
                "app_request_rate_rps": app_request_rate_rps,
                "app_request_burst": app_request_burst,
                "account_request_rate_rpm": account_request_rate_rpm,
                "request_budget_ms": request_budget_ms,
                "request_budget_max_ms": request_budget_max_ms,
                "response_write_timeout_s": response_write_timeout_s,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        memory_limit_mb = d.pop("memory_limit_mb")

        plan_memory_max_mb = d.pop("plan_memory_max_mb")

        ephemeral_disk_max_mb = d.pop("ephemeral_disk_max_mb")

        guest_vcpus = d.pop("guest_vcpus")

        cpu_limit_millicores = d.pop("cpu_limit_millicores")

        plan_cpu_max_millicores = d.pop("plan_cpu_max_millicores")

        cpu_weight = d.pop("cpu_weight")

        max_instances = d.pop("max_instances")

        concurrency_per_instance = d.pop("concurrency_per_instance")

        app_request_rate_rps = d.pop("app_request_rate_rps")

        app_request_burst = d.pop("app_request_burst")

        account_request_rate_rpm = d.pop("account_request_rate_rpm")

        request_budget_ms = d.pop("request_budget_ms")

        request_budget_max_ms = d.pop("request_budget_max_ms")

        response_write_timeout_s = d.pop("response_write_timeout_s")

        app_effective_limits = cls(
            memory_limit_mb=memory_limit_mb,
            plan_memory_max_mb=plan_memory_max_mb,
            ephemeral_disk_max_mb=ephemeral_disk_max_mb,
            guest_vcpus=guest_vcpus,
            cpu_limit_millicores=cpu_limit_millicores,
            plan_cpu_max_millicores=plan_cpu_max_millicores,
            cpu_weight=cpu_weight,
            max_instances=max_instances,
            concurrency_per_instance=concurrency_per_instance,
            app_request_rate_rps=app_request_rate_rps,
            app_request_burst=app_request_burst,
            account_request_rate_rpm=account_request_rate_rpm,
            request_budget_ms=request_budget_ms,
            request_budget_max_ms=request_budget_max_ms,
            response_write_timeout_s=response_write_timeout_s,
        )

        app_effective_limits.additional_properties = d
        return app_effective_limits

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
