from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="MFADisableEmailCancelResponse")


@_attrs_define
class MFADisableEmailCancelResponse:
    """How many pending email-assisted MFA disables were cancelled."""

    cancelled: int

    def to_dict(self) -> dict[str, Any]:
        cancelled = self.cancelled

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "cancelled": cancelled,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cancelled = d.pop("cancelled")

        mfa_disable_email_cancel_response = cls(
            cancelled=cancelled,
        )

        return mfa_disable_email_cancel_response
