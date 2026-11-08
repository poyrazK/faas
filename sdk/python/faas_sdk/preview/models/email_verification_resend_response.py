from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.email_verification_resend_response_status import (
    EmailVerificationResendResponseStatus,
    check_email_verification_resend_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EmailVerificationResendResponse")


@_attrs_define
class EmailVerificationResendResponse:
    """Result of `POST /v1/auth/verify-email/resend`."""

    status: EmailVerificationResendResponseStatus
    email: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        email = self.email

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if email is not UNSET:
            field_dict["email"] = email

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_email_verification_resend_response_status(d.pop("status"))

        email = d.pop("email", UNSET)

        email_verification_resend_response = cls(
            status=status,
            email=email,
        )

        email_verification_resend_response.additional_properties = d
        return email_verification_resend_response

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
