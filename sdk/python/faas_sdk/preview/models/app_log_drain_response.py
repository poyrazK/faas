from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_log_drain_response_auth_header_masked import (
    AppLogDrainResponseAuthHeaderMasked,
    check_app_log_drain_response_auth_header_masked,
)
from ..models.app_log_drain_response_kind import AppLogDrainResponseKind, check_app_log_drain_response_kind

T = TypeVar("T", bound="AppLogDrainResponse")


@_attrs_define
class AppLogDrainResponse:
    """Runtime log destination with sealed credentials represented by a mask."""

    id: str
    app_id: str
    account_id: UUID
    kind: AppLogDrainResponseKind
    target_url: str
    auth_header_masked: AppLogDrainResponseAuthHeaderMasked
    enabled: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        app_id = self.app_id

        account_id = str(self.account_id)

        kind: str = self.kind

        target_url = self.target_url

        auth_header_masked: str = self.auth_header_masked

        enabled = self.enabled

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "account_id": account_id,
                "kind": kind,
                "target_url": target_url,
                "auth_header_masked": auth_header_masked,
                "enabled": enabled,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        app_id = d.pop("app_id")

        account_id = UUID(d.pop("account_id"))

        kind = check_app_log_drain_response_kind(d.pop("kind"))

        target_url = d.pop("target_url")

        auth_header_masked = check_app_log_drain_response_auth_header_masked(d.pop("auth_header_masked"))

        enabled = d.pop("enabled")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        app_log_drain_response = cls(
            id=id,
            app_id=app_id,
            account_id=account_id,
            kind=kind,
            target_url=target_url,
            auth_header_masked=auth_header_masked,
            enabled=enabled,
            created_at=created_at,
            updated_at=updated_at,
        )

        app_log_drain_response.additional_properties = d
        return app_log_drain_response

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
