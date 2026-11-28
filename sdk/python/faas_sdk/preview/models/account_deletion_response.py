from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_deletion_response_status import (
    AccountDeletionResponseStatus,
    check_account_deletion_response_status,
)

T = TypeVar("T", bound="AccountDeletionResponse")


@_attrs_define
class AccountDeletionResponse:
    """Result of staging account deletion: status flips to `deleted_pending` and `restore_until` marks the end of the
    30-day grace window.

    """

    status: AccountDeletionResponseStatus
    scheduled_at: datetime.datetime
    restore_until: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        scheduled_at = self.scheduled_at.isoformat()

        restore_until = self.restore_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "scheduled_at": scheduled_at,
                "restore_until": restore_until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_account_deletion_response_status(d.pop("status"))

        scheduled_at = datetime.datetime.fromisoformat(d.pop("scheduled_at"))

        restore_until = datetime.datetime.fromisoformat(d.pop("restore_until"))

        account_deletion_response = cls(
            status=status,
            scheduled_at=scheduled_at,
            restore_until=restore_until,
        )

        account_deletion_response.additional_properties = d
        return account_deletion_response

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
