from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_secret_revocation_response_status import (
    AppSecretRevocationResponseStatus,
    check_app_secret_revocation_response_status,
)

if TYPE_CHECKING:
    from ..models.secret_revocation_target import SecretRevocationTarget


T = TypeVar("T", bound="AppSecretRevocationResponse")


@_attrs_define
class AppSecretRevocationResponse:
    """Value-free deletion status plus the exact active authorized runtime roster captured when deletion committed."""

    id: UUID
    scope: str
    key: str
    created_at: datetime.datetime
    status: AppSecretRevocationResponseStatus
    target_count: int
    acknowledged_count: int
    pending_count: int
    targets: list[SecretRevocationTarget]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        scope = self.scope

        key = self.key

        created_at = self.created_at.isoformat()

        status: str = self.status

        target_count = self.target_count

        acknowledged_count = self.acknowledged_count

        pending_count = self.pending_count

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "key": key,
                "created_at": created_at,
                "status": status,
                "target_count": target_count,
                "acknowledged_count": acknowledged_count,
                "pending_count": pending_count,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.secret_revocation_target import SecretRevocationTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        scope = d.pop("scope")

        key = d.pop("key")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = check_app_secret_revocation_response_status(d.pop("status"))

        target_count = d.pop("target_count")

        acknowledged_count = d.pop("acknowledged_count")

        pending_count = d.pop("pending_count")

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = SecretRevocationTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        app_secret_revocation_response = cls(
            id=id,
            scope=scope,
            key=key,
            created_at=created_at,
            status=status,
            target_count=target_count,
            acknowledged_count=acknowledged_count,
            pending_count=pending_count,
            targets=targets,
        )

        app_secret_revocation_response.additional_properties = d
        return app_secret_revocation_response

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
