from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.audit_log_entry import AuditLogEntry


T = TypeVar("T", bound="ListAuditLogResponse")


@_attrs_define
class ListAuditLogResponse:
    """Envelope for `GET /v1/audit-log` and `GET /v1/audit-log/all`
    (issue #755 / PR-6). `entries` is newest-first
    (`received_at DESC, id DESC`) so the dashboard can render
    top-of-list without re-sorting. `limit` echoes the
    effective limit applied by the handler so the SDK can
    render `showing N of M` without re-issuing the request.

    """

    entries: list[AuditLogEntry]
    limit: int
    """Effective page size applied (always 1..100; mirrors the customer and operator /v1/audit-log routes)."""
    next_before: str | Unset = UNSET
    """Opaque cursor for the next older page; omitted when this page reaches the end."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        limit = self.limit

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "entries": entries,
                "limit": limit,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.audit_log_entry import AuditLogEntry

        d = dict(src_dict)
        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = AuditLogEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        limit = d.pop("limit")

        next_before = d.pop("next_before", UNSET)

        list_audit_log_response = cls(
            entries=entries,
            limit=limit,
            next_before=next_before,
        )

        list_audit_log_response.additional_properties = d
        return list_audit_log_response

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
