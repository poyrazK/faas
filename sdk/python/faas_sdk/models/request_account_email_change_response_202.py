from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_account_email_change_response_202_status import (
    RequestAccountEmailChangeResponse202Status,
    check_request_account_email_change_response_202_status,
)

T = TypeVar("T", bound="RequestAccountEmailChangeResponse202")


@_attrs_define
class RequestAccountEmailChangeResponse202:
    status: RequestAccountEmailChangeResponse202Status
    email: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        email = self.email

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
                "email": email,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_request_account_email_change_response_202_status(d.pop("status"))

        email = d.pop("email")

        request_account_email_change_response_202 = cls(
            status=status,
            email=email,
        )

        request_account_email_change_response_202.additional_properties = d
        return request_account_email_change_response_202

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
