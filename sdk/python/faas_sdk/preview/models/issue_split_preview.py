from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_split_preview_application_status import (
    IssueSplitPreviewApplicationStatus,
    check_issue_split_preview_application_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_split_matcher import IssueSplitMatcher


T = TypeVar("T", bound="IssueSplitPreview")


@_attrs_define
class IssueSplitPreview:
    """Read-only counts for one original fingerprint, across all retained evidence. Unavailable lifetime history cannot be
    classified; no split or routing change is performed.

    """

    original_issue_id: UUID
    canonical_issue_id: UUID
    matcher: IssueSplitMatcher
    """Exact equality against sanitized stored evidence; every populated field must match. At least one nonempty
    selector is required. Empty fields are wildcards."""
    lifetime_event_count: int
    retained_event_count: int
    matching_event_count: int
    nonmatching_event_count: int
    unavailable_history_event_count: int
    matching_event_ids: list[UUID]
    """Newest matching occurrence IDs"""
    eligible_event_count: int | Unset = UNSET
    """Current retained matches a new rule would move; zero when application_status is not new-rule."""
    shadowed_event_count: int | Unset = UNSET
    """Retained selector matches claimed by already enabled rules."""
    eligible_event_ids: list[UUID] | Unset = UNSET
    """Newest eligible occurrence IDs after rule priority exclusions."""
    application_status: IssueSplitPreviewApplicationStatus | Unset = UNSET
    """Whether this matcher can create a new rule in the current snapshot; plan capacity is checked during
    application."""
    existing_rule_id: UUID | Unset = UNSET
    """Existing exact matcher returned by repeat creation without replay or re-enable."""
    existing_target_issue_id: UUID | Unset = UNSET
    """Stable branch of the existing exact matcher."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        original_issue_id = str(self.original_issue_id)

        canonical_issue_id = str(self.canonical_issue_id)

        matcher = self.matcher.to_dict()

        lifetime_event_count = self.lifetime_event_count

        retained_event_count = self.retained_event_count

        matching_event_count = self.matching_event_count

        nonmatching_event_count = self.nonmatching_event_count

        unavailable_history_event_count = self.unavailable_history_event_count

        matching_event_ids = []
        for matching_event_ids_item_data in self.matching_event_ids:
            matching_event_ids_item = str(matching_event_ids_item_data)
            matching_event_ids.append(matching_event_ids_item)

        eligible_event_count = self.eligible_event_count

        shadowed_event_count = self.shadowed_event_count

        eligible_event_ids: list[str] | Unset = UNSET
        if not isinstance(self.eligible_event_ids, Unset):
            eligible_event_ids = []
            for eligible_event_ids_item_data in self.eligible_event_ids:
                eligible_event_ids_item = str(eligible_event_ids_item_data)
                eligible_event_ids.append(eligible_event_ids_item)

        application_status: str | Unset = UNSET
        if not isinstance(self.application_status, Unset):
            application_status = self.application_status

        existing_rule_id: str | Unset = UNSET
        if not isinstance(self.existing_rule_id, Unset):
            existing_rule_id = str(self.existing_rule_id)

        existing_target_issue_id: str | Unset = UNSET
        if not isinstance(self.existing_target_issue_id, Unset):
            existing_target_issue_id = str(self.existing_target_issue_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "original_issue_id": original_issue_id,
                "canonical_issue_id": canonical_issue_id,
                "matcher": matcher,
                "lifetime_event_count": lifetime_event_count,
                "retained_event_count": retained_event_count,
                "matching_event_count": matching_event_count,
                "nonmatching_event_count": nonmatching_event_count,
                "unavailable_history_event_count": unavailable_history_event_count,
                "matching_event_ids": matching_event_ids,
            }
        )
        if eligible_event_count is not UNSET:
            field_dict["eligible_event_count"] = eligible_event_count
        if shadowed_event_count is not UNSET:
            field_dict["shadowed_event_count"] = shadowed_event_count
        if eligible_event_ids is not UNSET:
            field_dict["eligible_event_ids"] = eligible_event_ids
        if application_status is not UNSET:
            field_dict["application_status"] = application_status
        if existing_rule_id is not UNSET:
            field_dict["existing_rule_id"] = existing_rule_id
        if existing_target_issue_id is not UNSET:
            field_dict["existing_target_issue_id"] = existing_target_issue_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_split_matcher import IssueSplitMatcher

        d = dict(src_dict)
        original_issue_id = UUID(d.pop("original_issue_id"))

        canonical_issue_id = UUID(d.pop("canonical_issue_id"))

        matcher = IssueSplitMatcher.from_dict(d.pop("matcher"))

        lifetime_event_count = d.pop("lifetime_event_count")

        retained_event_count = d.pop("retained_event_count")

        matching_event_count = d.pop("matching_event_count")

        nonmatching_event_count = d.pop("nonmatching_event_count")

        unavailable_history_event_count = d.pop("unavailable_history_event_count")

        matching_event_ids = []
        _matching_event_ids = d.pop("matching_event_ids")
        for matching_event_ids_item_data in _matching_event_ids:
            matching_event_ids_item = UUID(matching_event_ids_item_data)

            matching_event_ids.append(matching_event_ids_item)

        eligible_event_count = d.pop("eligible_event_count", UNSET)

        shadowed_event_count = d.pop("shadowed_event_count", UNSET)

        _eligible_event_ids = d.pop("eligible_event_ids", UNSET)
        eligible_event_ids: list[UUID] | Unset = UNSET
        if _eligible_event_ids is not UNSET:
            eligible_event_ids = []
            for eligible_event_ids_item_data in _eligible_event_ids:
                eligible_event_ids_item = UUID(eligible_event_ids_item_data)

                eligible_event_ids.append(eligible_event_ids_item)

        _application_status = d.pop("application_status", UNSET)
        application_status: IssueSplitPreviewApplicationStatus | Unset
        if isinstance(_application_status, Unset):
            application_status = UNSET
        else:
            application_status = check_issue_split_preview_application_status(_application_status)

        _existing_rule_id = d.pop("existing_rule_id", UNSET)
        existing_rule_id: UUID | Unset
        if isinstance(_existing_rule_id, Unset):
            existing_rule_id = UNSET
        else:
            existing_rule_id = UUID(_existing_rule_id)

        _existing_target_issue_id = d.pop("existing_target_issue_id", UNSET)
        existing_target_issue_id: UUID | Unset
        if isinstance(_existing_target_issue_id, Unset):
            existing_target_issue_id = UNSET
        else:
            existing_target_issue_id = UUID(_existing_target_issue_id)

        issue_split_preview = cls(
            original_issue_id=original_issue_id,
            canonical_issue_id=canonical_issue_id,
            matcher=matcher,
            lifetime_event_count=lifetime_event_count,
            retained_event_count=retained_event_count,
            matching_event_count=matching_event_count,
            nonmatching_event_count=nonmatching_event_count,
            unavailable_history_event_count=unavailable_history_event_count,
            matching_event_ids=matching_event_ids,
            eligible_event_count=eligible_event_count,
            shadowed_event_count=shadowed_event_count,
            eligible_event_ids=eligible_event_ids,
            application_status=application_status,
            existing_rule_id=existing_rule_id,
            existing_target_issue_id=existing_target_issue_id,
        )

        issue_split_preview.additional_properties = d
        return issue_split_preview

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
