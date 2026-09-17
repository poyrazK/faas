from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_binding_response_mode import QueueBindingResponseMode, check_queue_binding_response_mode
from ..models.queue_binding_response_workload_class import (
    QueueBindingResponseWorkloadClass,
    check_queue_binding_response_workload_class,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.retry_policy_dto import RetryPolicyDTO


T = TypeVar("T", bound="QueueBindingResponse")


@_attrs_define
class QueueBindingResponse:
    """Durable queue-to-workload binding."""

    id: str
    app_id: str
    account_id: UUID
    name: str
    queue_name: str
    mode: QueueBindingResponseMode
    workload_class: QueueBindingResponseWorkloadClass
    enabled: bool
    max_concurrency: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    retry_policy: RetryPolicyDTO | Unset = UNSET
    """ADR-134 PR-B. Wire shape for dispatch.RetryPolicy. The handler
    decodes this DTO into a dispatch.RetryPolicy before persisting
    to invocations.retry_policy JSONB. Lives in pkg/api so the SDK
    can type the override without importing pkg/dispatch directly.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        account_id = str(self.account_id)

        name = self.name

        queue_name = self.queue_name

        mode: str = self.mode

        workload_class: str = self.workload_class

        enabled = self.enabled

        max_concurrency = self.max_concurrency

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        retry_policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry_policy, Unset):
            retry_policy = self.retry_policy.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "name": name,
                "queue_name": queue_name,
                "mode": mode,
                "workload_class": workload_class,
                "enabled": enabled,
                "max_concurrency": max_concurrency,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if retry_policy is not UNSET:
            field_dict["retry_policy"] = retry_policy

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.retry_policy_dto import RetryPolicyDTO

        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        account_id = UUID(d.pop("account_id"))

        name = d.pop("name")

        queue_name = d.pop("queue_name")

        mode = check_queue_binding_response_mode(d.pop("mode"))

        workload_class = check_queue_binding_response_workload_class(d.pop("workload_class"))

        enabled = d.pop("enabled")

        max_concurrency = d.pop("max_concurrency")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _retry_policy = d.pop("retry_policy", UNSET)
        retry_policy: RetryPolicyDTO | Unset
        if isinstance(_retry_policy, Unset):
            retry_policy = UNSET
        else:
            retry_policy = RetryPolicyDTO.from_dict(_retry_policy)

        queue_binding_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            name=name,
            queue_name=queue_name,
            mode=mode,
            workload_class=workload_class,
            enabled=enabled,
            max_concurrency=max_concurrency,
            created_at=created_at,
            updated_at=updated_at,
            retry_policy=retry_policy,
        )

        queue_binding_response.additional_properties = d
        return queue_binding_response

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
