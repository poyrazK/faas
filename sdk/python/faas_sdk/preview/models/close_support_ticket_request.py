from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CloseSupportTicketRequest")


@_attrs_define
class CloseSupportTicketRequest:
    """Close the current support resolution episode without extending a previous resolution timestamp."""

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

        close_support_ticket_request = cls(
            expected_revision=expected_revision,
        )

        return close_support_ticket_request
