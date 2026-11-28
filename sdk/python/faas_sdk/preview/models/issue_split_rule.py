from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.issue_split_matcher import IssueSplitMatcher


T = TypeVar("T", bound="IssueSplitRule")


@_attrs_define
class IssueSplitRule:
    """Immutable exact-match routing rule. Creating it moves eligible retained matches still assigned to the original
    source into an open branch. Earlier enabled rules take priority. Expired history remains on the source. Toggling
    affects future captures only; retries retain their existing routing identity.

    """

    id: UUID
    source_issue_id: UUID
    target_issue_id: UUID
    priority: int
    matcher: IssueSplitMatcher
    """Exact equality against sanitized stored evidence; every populated field must match. At least one nonempty
    selector is required. Empty fields are wildcards."""
    enabled: bool
    initial_moved_event_count: int
    unavailable_history_event_count: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_issue_id = str(self.source_issue_id)

        target_issue_id = str(self.target_issue_id)

        priority = self.priority

        matcher = self.matcher.to_dict()

        enabled = self.enabled

        initial_moved_event_count = self.initial_moved_event_count

        unavailable_history_event_count = self.unavailable_history_event_count

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_issue_id": source_issue_id,
                "target_issue_id": target_issue_id,
                "priority": priority,
                "matcher": matcher,
                "enabled": enabled,
                "initial_moved_event_count": initial_moved_event_count,
                "unavailable_history_event_count": unavailable_history_event_count,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_split_matcher import IssueSplitMatcher

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_issue_id = UUID(d.pop("source_issue_id"))

        target_issue_id = UUID(d.pop("target_issue_id"))

        priority = d.pop("priority")

        matcher = IssueSplitMatcher.from_dict(d.pop("matcher"))

        enabled = d.pop("enabled")

        initial_moved_event_count = d.pop("initial_moved_event_count")

        unavailable_history_event_count = d.pop("unavailable_history_event_count")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        issue_split_rule = cls(
            id=id,
            source_issue_id=source_issue_id,
            target_issue_id=target_issue_id,
            priority=priority,
            matcher=matcher,
            enabled=enabled,
            initial_moved_event_count=initial_moved_event_count,
            unavailable_history_event_count=unavailable_history_event_count,
            created_at=created_at,
            updated_at=updated_at,
        )

        issue_split_rule.additional_properties = d
        return issue_split_rule

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
