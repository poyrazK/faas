from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.project_environment_queue_binding_mode import (
    ProjectEnvironmentQueueBindingMode,
    check_project_environment_queue_binding_mode,
)
from ..models.project_environment_queue_binding_workload_class import (
    ProjectEnvironmentQueueBindingWorkloadClass,
    check_project_environment_queue_binding_workload_class,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="ProjectEnvironmentQueueBinding")


@_attrs_define
class ProjectEnvironmentQueueBinding:
    """Desired logical queue binding without runtime consumer identity or messages. HTTP consumers require push mode and a
    function workload.

    """

    name: str
    queue_name: str
    mode: ProjectEnvironmentQueueBindingMode
    workload_class: ProjectEnvironmentQueueBindingWorkloadClass
    enabled: bool
    max_concurrency: int
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
        name = self.name

        queue_name = self.queue_name

        mode: str = self.mode

        workload_class: str = self.workload_class

        enabled = self.enabled

        max_concurrency = self.max_concurrency

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "queue_name": queue_name,
                "mode": mode,
                "workload_class": workload_class,
                "enabled": enabled,
                "max_concurrency": max_concurrency,
            }
        )
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        name = d.pop("name")

        queue_name = d.pop("queue_name")

        mode = check_project_environment_queue_binding_mode(d.pop("mode"))

        workload_class = check_project_environment_queue_binding_workload_class(d.pop("workload_class"))

        enabled = d.pop("enabled")

        max_concurrency = d.pop("max_concurrency")

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        project_environment_queue_binding = cls(
            name=name,
            queue_name=queue_name,
            mode=mode,
            workload_class=workload_class,
            enabled=enabled,
            max_concurrency=max_concurrency,
            retry_policy=retry_policy,
        )

        return project_environment_queue_binding
