from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ReopenSupportTicketRequest")


@_attrs_define
class ReopenSupportTicketRequest:
    """Reopen within the saved policy window and count; starts a fresh episode using that policy snapshot."""

    expected_revision: int
    body: str

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        body = self.body

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "body": body,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        body = d.pop("body")

        reopen_support_ticket_request = cls(
            expected_revision=expected_revision,
            body=body,
        )

        return reopen_support_ticket_request
