from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_workload_profile_request_workload_class import (
    QueueWorkloadProfileRequestWorkloadClass,
    check_queue_workload_profile_request_workload_class,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="QueueWorkloadProfileRequest")


@_attrs_define
class QueueWorkloadProfileRequest:
    """Declarative setup for the common queue worker profile. The server
    reconciles the default push binding, consumer projection, and
    queue-depth scaling policy as one idempotent operation.

    """

    queue_name: str | Unset = "default"
    workload_class: QueueWorkloadProfileRequestWorkloadClass | Unset = UNSET
    """Defaults to the app workload class."""
    max_concurrency: int | Unset = 1
    target_depth: float | Unset = 10.0
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. The handler
    decodes this DTO into a dispatch.RetryPolicy before persisting
    to invocations.retry_policy JSONB. Lives in pkg/api so the SDK
    can type the override without importing pkg/dispatch directly.
    """
    force: bool | Unset = False
    """Replace an existing default binding that points at another queue."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        queue_name = self.queue_name

        workload_class: str | Unset = UNSET
        if not isinstance(self.workload_class, Unset):
            workload_class = self.workload_class

        max_concurrency = self.max_concurrency

        target_depth = self.target_depth

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        force = self.force

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if workload_class is not UNSET:
            field_dict["workload_class"] = workload_class
        if max_concurrency is not UNSET:
            field_dict["max_concurrency"] = max_concurrency
        if target_depth is not UNSET:
            field_dict["target_depth"] = target_depth
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy
        if force is not UNSET:
            field_dict["force"] = force

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        queue_name = d.pop("queue_name", UNSET)

        _workload_class = d.pop("workload_class", UNSET)
        workload_class: QueueWorkloadProfileRequestWorkloadClass | Unset
        if isinstance(_workload_class, Unset):
            workload_class = UNSET
        else:
            workload_class = check_queue_workload_profile_request_workload_class(_workload_class)

        max_concurrency = d.pop("max_concurrency", UNSET)

        target_depth = d.pop("target_depth", UNSET)

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        force = d.pop("force", UNSET)

        queue_workload_profile_request = cls(
            queue_name=queue_name,
            workload_class=workload_class,
            max_concurrency=max_concurrency,
            target_depth=target_depth,
            retry_policy=retry_policy,
            force=force,
        )

        queue_workload_profile_request.additional_properties = d
        return queue_workload_profile_request

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
