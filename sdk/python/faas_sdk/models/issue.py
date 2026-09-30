from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_state import IssueState, check_issue_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="Issue")


@_attrs_define
class Issue:
    """Durable failure group across releases in one app and environment."""

    id: UUID
    app_id: UUID
    environment: str
    fingerprint: str
    grouping_version: int
    title: str
    state: IssueState
    first_seen_at: datetime.datetime
    last_seen_at: datetime.datetime
    event_count: int
    regression_count: int
    assignee_account_id: UUID | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET
    fixed_deployment_id: UUID | Unset = UNSET
    fixed_deployment_created_at: datetime.datetime | Unset = UNSET
    ignored_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        environment = self.environment

        fingerprint = self.fingerprint

        grouping_version = self.grouping_version

        title = self.title

        state: str = self.state

        first_seen_at = self.first_seen_at.isoformat()

        last_seen_at = self.last_seen_at.isoformat()

        event_count = self.event_count

        regression_count = self.regression_count

        assignee_account_id: str | Unset = UNSET
        if not isinstance(self.assignee_account_id, Unset):
            assignee_account_id = str(self.assignee_account_id)

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        fixed_deployment_id: str | Unset = UNSET
        if not isinstance(self.fixed_deployment_id, Unset):
            fixed_deployment_id = str(self.fixed_deployment_id)

        fixed_deployment_created_at: str | Unset = UNSET
        if not isinstance(self.fixed_deployment_created_at, Unset):
            fixed_deployment_created_at = self.fixed_deployment_created_at.isoformat()

        ignored_until: str | Unset = UNSET
        if not isinstance(self.ignored_until, Unset):
            ignored_until = self.ignored_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "environment": environment,
                "fingerprint": fingerprint,
                "grouping_version": grouping_version,
                "title": title,
                "state": state,
                "first_seen_at": first_seen_at,
                "last_seen_at": last_seen_at,
                "event_count": event_count,
                "regression_count": regression_count,
            }
        )
        if assignee_account_id is not UNSET:
            field_dict["assignee_account_id"] = assignee_account_id
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at
        if fixed_deployment_id is not UNSET:
            field_dict["fixed_deployment_id"] = fixed_deployment_id
        if fixed_deployment_created_at is not UNSET:
            field_dict["fixed_deployment_created_at"] = fixed_deployment_created_at
        if ignored_until is not UNSET:
            field_dict["ignored_until"] = ignored_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        environment = d.pop("environment")

        fingerprint = d.pop("fingerprint")

        grouping_version = d.pop("grouping_version")

        title = d.pop("title")

        state = check_issue_state(d.pop("state"))

        first_seen_at = datetime.datetime.fromisoformat(d.pop("first_seen_at"))

        last_seen_at = datetime.datetime.fromisoformat(d.pop("last_seen_at"))

        event_count = d.pop("event_count")

        regression_count = d.pop("regression_count")

        _assignee_account_id = d.pop("assignee_account_id", UNSET)
        assignee_account_id: UUID | Unset
        if isinstance(_assignee_account_id, Unset):
            assignee_account_id = UNSET
        else:
            assignee_account_id = UUID(_assignee_account_id)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        _fixed_deployment_id = d.pop("fixed_deployment_id", UNSET)
        fixed_deployment_id: UUID | Unset
        if isinstance(_fixed_deployment_id, Unset):
            fixed_deployment_id = UNSET
        else:
            fixed_deployment_id = UUID(_fixed_deployment_id)

        _fixed_deployment_created_at = d.pop("fixed_deployment_created_at", UNSET)
        fixed_deployment_created_at: datetime.datetime | Unset
        if isinstance(_fixed_deployment_created_at, Unset):
            fixed_deployment_created_at = UNSET
        else:
            fixed_deployment_created_at = datetime.datetime.fromisoformat(_fixed_deployment_created_at)

        _ignored_until = d.pop("ignored_until", UNSET)
        ignored_until: datetime.datetime | Unset
        if isinstance(_ignored_until, Unset):
            ignored_until = UNSET
        else:
            ignored_until = datetime.datetime.fromisoformat(_ignored_until)

        issue = cls(
            id=id,
            app_id=app_id,
            environment=environment,
            fingerprint=fingerprint,
            grouping_version=grouping_version,
            title=title,
            state=state,
            first_seen_at=first_seen_at,
            last_seen_at=last_seen_at,
            event_count=event_count,
            regression_count=regression_count,
            assignee_account_id=assignee_account_id,
            resolved_at=resolved_at,
            fixed_deployment_id=fixed_deployment_id,
            fixed_deployment_created_at=fixed_deployment_created_at,
            ignored_until=ignored_until,
        )

        issue.additional_properties = d
        return issue

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
