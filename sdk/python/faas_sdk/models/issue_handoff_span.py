from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueHandoffSpan")


@_attrs_define
class IssueHandoffSpan:
    """Sanitized span identity, label, status, and duration from retained request evidence."""

    span_id: str
    name: str
    duration_nanos: int
    parent_span_id: str | Unset = UNSET
    kind: str | Unset = UNSET
    status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        span_id = self.span_id

        name = self.name

        duration_nanos = self.duration_nanos

        parent_span_id = self.parent_span_id

        kind = self.kind

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "span_id": span_id,
                "name": name,
                "duration_nanos": duration_nanos,
            }
        )
        if parent_span_id is not UNSET:
            field_dict["parent_span_id"] = parent_span_id
        if kind is not UNSET:
            field_dict["kind"] = kind
        if status is not UNSET:
            field_dict["status"] = status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        span_id = d.pop("span_id")

        name = d.pop("name")

        duration_nanos = d.pop("duration_nanos")

        parent_span_id = d.pop("parent_span_id", UNSET)

        kind = d.pop("kind", UNSET)

        status = d.pop("status", UNSET)

        issue_handoff_span = cls(
            span_id=span_id,
            name=name,
            duration_nanos=duration_nanos,
            parent_span_id=parent_span_id,
            kind=kind,
            status=status,
        )

        issue_handoff_span.additional_properties = d
        return issue_handoff_span

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
