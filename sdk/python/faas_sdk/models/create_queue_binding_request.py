from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_queue_binding_request_mode import (
    CreateQueueBindingRequestMode,
    check_create_queue_binding_request_mode,
)
from ..models.create_queue_binding_request_workload_class import (
    CreateQueueBindingRequestWorkloadClass,
    check_create_queue_binding_request_workload_class,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="CreateQueueBindingRequest")


@_attrs_define
class CreateQueueBindingRequest:
    """Durable mapping from a logical queue to a worker/job workload."""

    name: str
    queue_name: str
    mode: CreateQueueBindingRequestMode | Unset = "pull"
    workload_class: CreateQueueBindingRequestWorkloadClass | Unset = "worker"
    enabled: bool | Unset = True
    max_concurrency: int | Unset = 1
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. max_attempts
    is a requested total-attempt count; zero inherits the applicable
    account plan and never means unlimited. Durable invocation
    producers materialize the effective plan-capped value, and the
    scheduler re-clamps it at dispatch time to account for later plan
    downgrades. Lives in pkg/api so the SDK can type the policy
    without importing pkg/dispatch directly.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        queue_name = self.queue_name

        mode: str | Unset = UNSET
        if not isinstance(self.mode, Unset):
            mode = self.mode

        workload_class: str | Unset = UNSET
        if not isinstance(self.workload_class, Unset):
            workload_class = self.workload_class

        enabled = self.enabled

        max_concurrency = self.max_concurrency

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "queue_name": queue_name,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode
        if workload_class is not UNSET:
            field_dict["workload_class"] = workload_class
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
        name = d.pop("name")

        queue_name = d.pop("queue_name")

        _mode = d.pop("mode", UNSET)
        mode: CreateQueueBindingRequestMode | Unset
        if isinstance(_mode, Unset):
            mode = UNSET
        else:
            mode = check_create_queue_binding_request_mode(_mode)

        _workload_class = d.pop("workload_class", UNSET)
        workload_class: CreateQueueBindingRequestWorkloadClass | Unset
        if isinstance(_workload_class, Unset):
            workload_class = UNSET
        else:
            workload_class = check_create_queue_binding_request_workload_class(_workload_class)

        enabled = d.pop("enabled", UNSET)

        max_concurrency = d.pop("max_concurrency", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        create_queue_binding_request = cls(
            name=name,
            queue_name=queue_name,
            mode=mode,
            workload_class=workload_class,
            enabled=enabled,
            max_concurrency=max_concurrency,
            retry_policy=retry_policy,
        )

        create_queue_binding_request.additional_properties = d
        return create_queue_binding_request

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
