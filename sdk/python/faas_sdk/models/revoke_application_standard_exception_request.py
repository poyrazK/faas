from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RevokeApplicationStandardExceptionRequest")


@_attrs_define
class RevokeApplicationStandardExceptionRequest:
    """Current enrollment revision authorizing revocation of the selected exception."""

    expected_revision: int

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        revoke_application_standard_exception_request = cls(
            expected_revision=expected_revision,
        )

        return revoke_application_standard_exception_request
