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
    eligible account.

    """

    assignee_account_id: UUID
    exception_type: str | Unset = UNSET
    """Exact exception type match."""
    source_kind: IssueOwnershipRuleSourceKind | Unset = UNSET
    route_prefix: str | Unset = UNSET
    """Path prefix; matches only at a path-segment boundary."""

    def to_dict(self) -> dict[str, Any]:
        assignee_account_id = str(self.assignee_account_id)

        exception_type = self.exception_type

        source_kind: str | Unset = UNSET
        if not isinstance(self.source_kind, Unset):
            source_kind = self.source_kind

        route_prefix = self.route_prefix

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "assignee_account_id": assignee_account_id,
            }
        )
        if exception_type is not UNSET:
            field_dict["exception_type"] = exception_type
        if source_kind is not UNSET:
            field_dict["source_kind"] = source_kind
        if route_prefix is not UNSET:
            field_dict["route_prefix"] = route_prefix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        assignee_account_id = UUID(d.pop("assignee_account_id"))

        exception_type = d.pop("exception_type", UNSET)

        _source_kind = d.pop("source_kind", UNSET)
        source_kind: IssueOwnershipRuleSourceKind | Unset
        if isinstance(_source_kind, Unset):
            source_kind = UNSET
        else:
            source_kind = check_issue_ownership_rule_source_kind(_source_kind)

        route_prefix = d.pop("route_prefix", UNSET)

        issue_ownership_rule = cls(
            assignee_account_id=assignee_account_id,
            exception_type=exception_type,
            source_kind=source_kind,
            route_prefix=route_prefix,
        )

        return issue_ownership_rule
