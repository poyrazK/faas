from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CancelDeploymentResponse200")


@_attrs_define
class CancelDeploymentResponse200:
    id: str | Unset = UNSET
    status: str | Unset = UNSET
    cancelled_at: datetime.datetime | Unset = UNSET
    cancel_reason: str | Unset = UNSET
    cancelled_builds: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        status = self.status

        cancelled_at: str | Unset = UNSET
        if not isinstance(self.cancelled_at, Unset):
            cancelled_at = self.cancelled_at.isoformat()

        cancel_reason = self.cancel_reason

        cancelled_builds: list[str] | Unset = UNSET
        if not isinstance(self.cancelled_builds, Unset):
            cancelled_builds = self.cancelled_builds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if id is not UNSET:
            field_dict["id"] = id
        if status is not UNSET:
            field_dict["status"] = status
        if cancelled_at is not UNSET:
            field_dict["cancelled_at"] = cancelled_at
        if cancel_reason is not UNSET:
            field_dict["cancel_reason"] = cancel_reason
        if cancelled_builds is not UNSET:
            field_dict["cancelled_builds"] = cancelled_builds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id", UNSET)

        status = d.pop("status", UNSET)

        _cancelled_at = d.pop("cancelled_at", UNSET)
        cancelled_at: datetime.datetime | Unset
        if isinstance(_cancelled_at, Unset):
            cancelled_at = UNSET
        else:
            cancelled_at = datetime.datetime.fromisoformat(_cancelled_at)

        cancel_reason = d.pop("cancel_reason", UNSET)

        cancelled_builds = cast(list[str], d.pop("cancelled_builds", UNSET))

        cancel_deployment_response_200 = cls(
            id=id,
            status=status,
            cancelled_at=cancelled_at,
            cancel_reason=cancel_reason,
            cancelled_builds=cancelled_builds,
        )

        cancel_deployment_response_200.additional_properties = d
        return cancel_deployment_response_200

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
