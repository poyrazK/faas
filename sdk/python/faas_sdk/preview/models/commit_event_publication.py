from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CommitEventPublication")


@_attrs_define
class CommitEventPublication:
    """Retained event-publication acceptance and a guarded URL for subscriber delivery evidence."""

    acceptance_id: int
    source: str
    id: str
    accepted_at: datetime.datetime
    receipt_url: str
    """Read-only event receipt with an acceptance-time guard; exposes paginated subscriber delivery evidence while
    retained."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        acceptance_id = self.acceptance_id

        source = self.source

        id = self.id

        accepted_at = self.accepted_at.isoformat()

        receipt_url = self.receipt_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "acceptance_id": acceptance_id,
                "source": source,
                "id": id,
                "accepted_at": accepted_at,
                "receipt_url": receipt_url,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        acceptance_id = d.pop("acceptance_id")

        source = d.pop("source")

        id = d.pop("id")

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        receipt_url = d.pop("receipt_url")

        commit_event_publication = cls(
            acceptance_id=acceptance_id,
            source=source,
            id=id,
            accepted_at=accepted_at,
            receipt_url=receipt_url,
        )

        commit_event_publication.additional_properties = d
        return commit_event_publication

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
