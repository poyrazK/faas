from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue import Issue
    from ..models.issue_activity import IssueActivity
    from ..models.issue_impact import IssueImpact
    from ..models.issue_occurrence import IssueOccurrence
    from ..models.issue_release import IssueRelease
    from ..models.issue_split_rule import IssueSplitRule


T = TypeVar("T", bound="IssueDetail")


@_attrs_define
class IssueDetail:
    """Canonical issue metadata and independently paginated combined history. Linked IDs resolve to their current canonical
    issue; original identities and counters appear in group_members.

    """

    issue: Issue
    """Durable failure group across releases in one app and environment."""
    events: list[IssueOccurrence]
    releases: list[IssueRelease]
    activity: list[IssueActivity]
    impact: IssueImpact
    """Observed retained customer impact within an explicit time window; unknown identity remains unattributed."""
    split_rules: list[IssueSplitRule] | Unset = UNSET
    """Routing rules associated with the displayed original identities or derived branches; lower priority wins per
    source."""
    group_members: list[Issue] | Unset = UNSET
    """Original fingerprint identities with their own lifetime counters."""
    next_event_cursor: str | Unset = UNSET
    next_release_cursor: str | Unset = UNSET
    next_activity_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        issue = self.issue.to_dict()

        events = []
        for events_item_data in self.events:
            events_item = events_item_data.to_dict()
            events.append(events_item)

        releases = []
        for releases_item_data in self.releases:
            releases_item = releases_item_data.to_dict()
            releases.append(releases_item)

        activity = []
        for activity_item_data in self.activity:
            activity_item = activity_item_data.to_dict()
            activity.append(activity_item)

        impact = self.impact.to_dict()

        split_rules: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.split_rules, Unset):
            split_rules = []
            for split_rules_item_data in self.split_rules:
                split_rules_item = split_rules_item_data.to_dict()
                split_rules.append(split_rules_item)

        group_members: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.group_members, Unset):
            group_members = []
            for group_members_item_data in self.group_members:
                group_members_item = group_members_item_data.to_dict()
                group_members.append(group_members_item)

        next_event_cursor = self.next_event_cursor

        next_release_cursor = self.next_release_cursor

        next_activity_cursor = self.next_activity_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "issue": issue,
                "events": events,
                "releases": releases,
                "activity": activity,
                "impact": impact,
            }
        )
        if split_rules is not UNSET:
            field_dict["split_rules"] = split_rules
        if group_members is not UNSET:
            field_dict["group_members"] = group_members
        if next_event_cursor is not UNSET:
            field_dict["next_event_cursor"] = next_event_cursor
        if next_release_cursor is not UNSET:
            field_dict["next_release_cursor"] = next_release_cursor
        if next_activity_cursor is not UNSET:
            field_dict["next_activity_cursor"] = next_activity_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue import Issue
        from ..models.issue_activity import IssueActivity
        from ..models.issue_impact import IssueImpact
        from ..models.issue_occurrence import IssueOccurrence
        from ..models.issue_release import IssueRelease
        from ..models.issue_split_rule import IssueSplitRule

        d = dict(src_dict)
        issue = Issue.from_dict(d.pop("issue"))

        events = []
        _events = d.pop("events")
        for events_item_data in _events:
            events_item = IssueOccurrence.from_dict(events_item_data)

            events.append(events_item)

        releases = []
        _releases = d.pop("releases")
        for releases_item_data in _releases:
            releases_item = IssueRelease.from_dict(releases_item_data)

            releases.append(releases_item)

        activity = []
        _activity = d.pop("activity")
        for activity_item_data in _activity:
            activity_item = IssueActivity.from_dict(activity_item_data)

            activity.append(activity_item)

        impact = IssueImpact.from_dict(d.pop("impact"))

        _split_rules = d.pop("split_rules", UNSET)
        split_rules: list[IssueSplitRule] | Unset = UNSET
        if _split_rules is not UNSET:
            split_rules = []
            for split_rules_item_data in _split_rules:
                split_rules_item = IssueSplitRule.from_dict(split_rules_item_data)

                split_rules.append(split_rules_item)

        _group_members = d.pop("group_members", UNSET)
        group_members: list[Issue] | Unset = UNSET
        if _group_members is not UNSET:
            group_members = []
            for group_members_item_data in _group_members:
                group_members_item = Issue.from_dict(group_members_item_data)

                group_members.append(group_members_item)

        next_event_cursor = d.pop("next_event_cursor", UNSET)

        next_release_cursor = d.pop("next_release_cursor", UNSET)

        next_activity_cursor = d.pop("next_activity_cursor", UNSET)

        issue_detail = cls(
            issue=issue,
            events=events,
            releases=releases,
            activity=activity,
            impact=impact,
            split_rules=split_rules,
            group_members=group_members,
            next_event_cursor=next_event_cursor,
            next_release_cursor=next_release_cursor,
            next_activity_cursor=next_activity_cursor,
        )

        issue_detail.additional_properties = d
        return issue_detail

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
