from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.issue_saved_view_filters_sort import IssueSavedViewFiltersSort, check_issue_saved_view_filters_sort
from ..models.issue_saved_view_filters_state import IssueSavedViewFiltersState, check_issue_saved_view_filters_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="IssueSavedViewFilters")


@_attrs_define
class IssueSavedViewFilters:
    """Supported query filters retained by a shared application issue view."""

    state: IssueSavedViewFiltersState | Unset = UNSET
    """Omit to include every state."""
    environment: str | Unset = UNSET
    """Omit to include every issue environment."""
    assignee: str | Unset = UNSET
    """me, unassigned, or an eligible account UUID. Omit for all owners. me resolves for each authenticated viewer."""
    sort: IssueSavedViewFiltersSort | Unset = "recent"
    min_customers: int | Unset = 0
    tag: str | Unset = UNSET
    """Exact allowlisted operational key=value in retained occurrences over 24h; value is limited to 128 UTF-8
    bytes."""
    team: UUID | Unset = UNSET
    """App-scoped team inbox; mutually exclusive with assignee."""

    def to_dict(self) -> dict[str, Any]:
        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        environment = self.environment

        assignee = self.assignee

        sort: str | Unset = UNSET
        if not isinstance(self.sort, Unset):
            sort = self.sort

        min_customers = self.min_customers

        tag = self.tag

        team: str | Unset = UNSET
        if not isinstance(self.team, Unset):
            team = str(self.team)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if state is not UNSET:
            field_dict["state"] = state
        if environment is not UNSET:
            field_dict["environment"] = environment
        if assignee is not UNSET:
            field_dict["assignee"] = assignee
        if sort is not UNSET:
            field_dict["sort"] = sort
        if min_customers is not UNSET:
            field_dict["min_customers"] = min_customers
        if tag is not UNSET:
            field_dict["tag"] = tag
        if team is not UNSET:
            field_dict["team"] = team

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _state = d.pop("state", UNSET)
        state: IssueSavedViewFiltersState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_issue_saved_view_filters_state(_state)

        environment = d.pop("environment", UNSET)

        assignee = d.pop("assignee", UNSET)

        _sort = d.pop("sort", UNSET)
        sort: IssueSavedViewFiltersSort | Unset
        if isinstance(_sort, Unset):
            sort = UNSET
        else:
            sort = check_issue_saved_view_filters_sort(_sort)

        min_customers = d.pop("min_customers", UNSET)

        tag = d.pop("tag", UNSET)

        _team = d.pop("team", UNSET)
        team: UUID | Unset
        if isinstance(_team, Unset):
            team = UNSET
        else:
            team = UUID(_team)

        issue_saved_view_filters = cls(
            state=state,
            environment=environment,
            assignee=assignee,
            sort=sort,
            min_customers=min_customers,
            tag=tag,
            team=team,
        )

        return issue_saved_view_filters
