from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.secret_revocation_target_error_code import (
    SecretRevocationTargetErrorCode,
    check_secret_revocation_target_error_code,
)
from ..models.secret_revocation_target_reload_support import (
    SecretRevocationTargetReloadSupport,
    check_secret_revocation_target_reload_support,
)
from ..models.secret_revocation_target_status import SecretRevocationTargetStatus, check_secret_revocation_target_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="SecretRevocationTarget")


@_attrs_define
class SecretRevocationTarget:
    """Non-sensitive snapshot of one authorized workload's deletion acknowledgement state."""

    instance_id: UUID
    runtime_state: str
    reload_support: SecretRevocationTargetReloadSupport
    status: SecretRevocationTargetStatus
    workload_name: str | Unset = UNSET
    ack_revision: str | Unset = UNSET
    ack_at: datetime.datetime | Unset = UNSET
    error_code: SecretRevocationTargetErrorCode | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        instance_id = str(self.instance_id)

        runtime_state = self.runtime_state

        reload_support: str = self.reload_support

        status: str = self.status

        workload_name = self.workload_name

        ack_revision = self.ack_revision

        ack_at: str | Unset = UNSET
        if not isinstance(self.ack_at, Unset):
            ack_at = self.ack_at.isoformat()

        error_code: str | Unset = UNSET
        if not isinstance(self.error_code, Unset):
            error_code = self.error_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "instance_id": instance_id,
                "runtime_state": runtime_state,
                "reload_support": reload_support,
                "status": status,
            }
        )
        if workload_name is not UNSET:
            field_dict["workload_name"] = workload_name
        if ack_revision is not UNSET:
            field_dict["ack_revision"] = ack_revision
        if ack_at is not UNSET:
            field_dict["ack_at"] = ack_at
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        instance_id = UUID(d.pop("instance_id"))

        runtime_state = d.pop("runtime_state")

        reload_support = check_secret_revocation_target_reload_support(d.pop("reload_support"))

        status = check_secret_revocation_target_status(d.pop("status"))

        workload_name = d.pop("workload_name", UNSET)

        ack_revision = d.pop("ack_revision", UNSET)

        _ack_at = d.pop("ack_at", UNSET)
        ack_at: datetime.datetime | Unset
        if isinstance(_ack_at, Unset):
            ack_at = UNSET
        else:
            ack_at = datetime.datetime.fromisoformat(_ack_at)

        _error_code = d.pop("error_code", UNSET)
        error_code: SecretRevocationTargetErrorCode | Unset
        if isinstance(_error_code, Unset):
            error_code = UNSET
        else:
            error_code = check_secret_revocation_target_error_code(_error_code)

        secret_revocation_target = cls(
            instance_id=instance_id,
            runtime_state=runtime_state,
            reload_support=reload_support,
            status=status,
            workload_name=workload_name,
            ack_revision=ack_revision,
            ack_at=ack_at,
            error_code=error_code,
        )

        secret_revocation_target.additional_properties = d
        return secret_revocation_target

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
