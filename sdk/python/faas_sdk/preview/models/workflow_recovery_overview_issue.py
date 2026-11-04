from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_overview_issue_scope import (
    WorkflowRecoveryOverviewIssueScope,
    check_workflow_recovery_overview_issue_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryOverviewIssue")


@_attrs_define
class WorkflowRecoveryOverviewIssue:
    """One recovery issue with evidence and the next available operator action."""

    scope: WorkflowRecoveryOverviewIssueScope
    reason: str
    policy_version: int
    inspection_url: str
    review_id: UUID | Unset = UNSET
    job_id: UUID | Unset = UNSET
    retry_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        reason = self.reason

        policy_version = self.policy_version

        inspection_url = self.inspection_url

        review_id: str | Unset = UNSET
        if not isinstance(self.review_id, Unset):
            review_id = str(self.review_id)

        job_id: str | Unset = UNSET
        if not isinstance(self.job_id, Unset):
            job_id = str(self.job_id)

        retry_at: str | Unset = UNSET
        if not isinstance(self.retry_at, Unset):
            retry_at = self.retry_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scope": scope,
                "reason": reason,
                "policy_version": policy_version,
                "inspection_url": inspection_url,
            }
        )
        if review_id is not UNSET:
            field_dict["review_id"] = review_id
        if job_id is not UNSET:
            field_dict["job_id"] = job_id
        if retry_at is not UNSET:
            field_dict["retry_at"] = retry_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_workflow_recovery_overview_issue_scope(d.pop("scope"))

        reason = d.pop("reason")

        policy_version = d.pop("policy_version")

        inspection_url = d.pop("inspection_url")

        _review_id = d.pop("review_id", UNSET)
        review_id: UUID | Unset
        if isinstance(_review_id, Unset):
            review_id = UNSET
        else:
            review_id = UUID(_review_id)

        _job_id = d.pop("job_id", UNSET)
        job_id: UUID | Unset
        if isinstance(_job_id, Unset):
            job_id = UNSET
        else:
            job_id = UUID(_job_id)

        _retry_at = d.pop("retry_at", UNSET)
        retry_at: datetime.datetime | Unset
        if isinstance(_retry_at, Unset):
            retry_at = UNSET
        else:
            retry_at = datetime.datetime.fromisoformat(_retry_at)

        workflow_recovery_overview_issue = cls(
            scope=scope,
            reason=reason,
            policy_version=policy_version,
            inspection_url=inspection_url,
            review_id=review_id,
            job_id=job_id,
            retry_at=retry_at,
        )

        workflow_recovery_overview_issue.additional_properties = d
        return workflow_recovery_overview_issue

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
