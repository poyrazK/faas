from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.incident_note_kind import IncidentNoteKind, check_incident_note_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="IncidentNote")


@_attrs_define
class IncidentNote:
    """An account's acknowledgement (no body) or note on an incident."""

    id: UUID
    kind: IncidentNoteKind
    author: str
    """An email or api_key:<id>."""
    created_at: datetime.datetime
    body: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind: str = self.kind

        author = self.author

        created_at = self.created_at.isoformat()

        body = self.body

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "author": author,
                "created_at": created_at,
            }
        )
        if body is not UNSET:
            field_dict["body"] = body

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = check_incident_note_kind(d.pop("kind"))

        author = d.pop("author")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        body = d.pop("body", UNSET)

        incident_note = cls(
            id=id,
            kind=kind,
            author=author,
            created_at=created_at,
            body=body,
        )

        incident_note.additional_properties = d
        return incident_note

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
