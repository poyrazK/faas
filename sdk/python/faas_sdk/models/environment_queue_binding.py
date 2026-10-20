from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.environment_queue_binding_mode import EnvironmentQueueBindingMode, check_environment_queue_binding_mode
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="EnvironmentQueueBinding")


@_attrs_define
class EnvironmentQueueBinding:
    """Atomic queue binding intent in the named environment. Reviewed adoption preserves the original binding and consumer
    IDs and delivery history. Queue pruning and retired identity recovery require a separate reviewed disposition.

    """

    queue_name: str
    workload_class: str
    """worker or job, or http for push-only bindings on function workloads"""
    mode: EnvironmentQueueBindingMode | Unset = UNSET
    enabled: bool | Unset = UNSET
    max_concurrency: int | Unset = UNSET
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """

    def to_dict(self) -> dict[str, Any]:
        queue_name = self.queue_name

        workload_class = self.workload_class

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        enabled = self.enabled

        max_concurrency = self.max_concurrency

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "queue_name": queue_name,
                "workload_class": workload_class,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        queue_name = d.pop("queue_name")

        workload_class = d.pop("workload_class")

        _mode = d.pop("mode", UNSET)
        mode: EnvironmentQueueBindingMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_environment_queue_binding_mode(_mode)

        enabled = d.pop("enabled", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        environment_queue_binding = cls(
            queue_name=queue_name,
            workload_class=workload_class,
            mode=mode,
            enabled=enabled,
            max_concurrency=max_concurrency,
            retry_policy=retry_policy,
        )

        return environment_queue_binding
