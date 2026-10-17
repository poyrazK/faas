from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_binding_canary_status import QueueBindingCanaryStatus, check_queue_binding_canary_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueBindingCanary")


@_attrs_define
class QueueBindingCanary:
    """Retained deployment-bound receipt for an explicitly authorized push queue completion probe."""

    id: UUID
    binding: str
    deployment_id: UUID
    scope: str
    status: QueueBindingCanaryStatus
    published_at: datetime.datetime
    expires_at: datetime.datetime
    reason: str | Unset = UNSET
    delivered_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        binding = self.binding

        deployment_id = str(self.deployment_id)

        scope = self.scope

        status: str = self.status

        published_at = self.published_at.isoformat()

        expires_at = self.expires_at.isoformat()

        reason = self.reason

        delivered_at: str | Unset = UNSET
        if not isinstance(self.delivered_at, Unset):
            delivered_at = self.delivered_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "binding": binding,
                "deployment_id": deployment_id,
                "scope": scope,
                "status": status,
                "published_at": published_at,
                "expires_at": expires_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if delivered_at is not UNSET:
            field_dict["delivered_at"] = delivered_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        binding = d.pop("binding")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        status = check_queue_binding_canary_status(d.pop("status"))

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        reason = d.pop("reason", UNSET)

        _delivered_at = d.pop("delivered_at", UNSET)
        delivered_at: datetime.datetime | Unset
        if isinstance(_delivered_at, Unset):
            delivered_at = UNSET
        else:
            delivered_at = datetime.datetime.fromisoformat(_delivered_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        queue_binding_canary = cls(
            id=id,
            binding=binding,
            deployment_id=deployment_id,
            scope=scope,
            status=status,
            published_at=published_at,
            expires_at=expires_at,
            reason=reason,
            delivered_at=delivered_at,
            completed_at=completed_at,
        )

        queue_binding_canary.additional_properties = d
        return queue_binding_canary

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
