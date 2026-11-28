from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.issue_ownership_rule_source_kind import (
    IssueOwnershipRuleSourceKind,
    check_issue_ownership_rule_source_kind,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueOwnershipRule")


@_attrs_define
class IssueOwnershipRule:
    """Provide at least one matcher. All populated matchers are ANDed; the first matching rule assigns a new issue to one
    eligible account or team. Exactly one assignment target is required.

    """

    assignee_account_id: UUID | Unset = UNSET
    exception_type: str | Unset = UNSET
    """Exact exception type match."""
    source_kind: IssueOwnershipRuleSourceKind | Unset = UNSET
    route_prefix: str | Unset = UNSET
    """Path prefix; matches only at a path-segment boundary."""
    source_path_prefix: str | Unset = UNSET
    """Local source path prefix (256 UTF-8 bytes); matches any application frame in the exception or its causes at
    a path-segment boundary. Backslashes and dot segments normalize; parent traversal, URLs, queries, and fragments
    are rejected. No glob or filesystem access."""
    tag: str | Unset = UNSET
    """Exact allowlisted operational key=value matcher against sanitized reported tags; value <=128 UTF-8 bytes.
    Missing keys do not match an empty value."""
    assignee_team_id: UUID | Unset = UNSET
    """Team selected by this ownership rule; cannot be combined with an account assignment."""

    def to_dict(self) -> dict[str, Any]:
        assignee_account_id: str | Unset = UNSET
        if not isinstance(self.assignee_account_id, Unset):
            assignee_account_id = str(self.assignee_account_id)

        exception_type = self.exception_type

        source_kind: str | Unset = UNSET
        if not isinstance(self.source_kind, Unset):
            source_kind = self.source_kind

        route_prefix = self.route_prefix

        source_path_prefix = self.source_path_prefix

        tag = self.tag

        assignee_team_id: str | Unset = UNSET
        if not isinstance(self.assignee_team_id, Unset):
            assignee_team_id = str(self.assignee_team_id)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if assignee_account_id is not UNSET:
            field_dict["assignee_account_id"] = assignee_account_id
        if exception_type is not UNSET:
            field_dict["exception_type"] = exception_type
        if source_kind is not UNSET:
            field_dict["source_kind"] = source_kind
        if route_prefix is not UNSET:
            field_dict["route_prefix"] = route_prefix
        if source_path_prefix is not UNSET:
            field_dict["source_path_prefix"] = source_path_prefix
        if tag is not UNSET:
            field_dict["tag"] = tag
        if assignee_team_id is not UNSET:
            field_dict["assignee_team_id"] = assignee_team_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _assignee_account_id = d.pop("assignee_account_id", UNSET)
        assignee_account_id: UUID | Unset
        if isinstance(_assignee_account_id, Unset):
            assignee_account_id = UNSET
        else:
            assignee_account_id = UUID(_assignee_account_id)

        exception_type = d.pop("exception_type", UNSET)

        _source_kind = d.pop("source_kind", UNSET)
        source_kind: IssueOwnershipRuleSourceKind | Unset
        if isinstance(_source_kind, Unset):
            source_kind = UNSET
        else:
            source_kind = check_issue_ownership_rule_source_kind(_source_kind)

        route_prefix = d.pop("route_prefix", UNSET)

        source_path_prefix = d.pop("source_path_prefix", UNSET)

        tag = d.pop("tag", UNSET)

        _assignee_team_id = d.pop("assignee_team_id", UNSET)
        assignee_team_id: UUID | Unset
        if isinstance(_assignee_team_id, Unset):
            assignee_team_id = UNSET
        else:
            assignee_team_id = UUID(_assignee_team_id)

        issue_ownership_rule = cls(
            assignee_account_id=assignee_account_id,
            exception_type=exception_type,
            source_kind=source_kind,
            route_prefix=route_prefix,
            source_path_prefix=source_path_prefix,
            tag=tag,
            assignee_team_id=assignee_team_id,
        )

        return issue_ownership_rule
