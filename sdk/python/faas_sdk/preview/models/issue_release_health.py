from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_release_health_request_coverage import (
    IssueReleaseHealthRequestCoverage,
    check_issue_release_health_request_coverage,
)

T = TypeVar("T", bound="IssueReleaseHealth")


@_attrs_define
class IssueReleaseHealth:
    """Observed issue and occurrence counts for one deployment within the requested environment."""

    deployment_id: UUID
    commit_sha: str
    created_at: datetime.datetime
    observed_requests: int
    http_5xx_requests: int
    http_5xx_rate: float | None
    request_coverage: IssueReleaseHealthRequestCoverage
    observed_issue_events: int
    observed_issue_groups: int
    new_issue_groups: int
    regressed_issue_groups: int
    identified_customers: int
    unattributed_events: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        commit_sha = self.commit_sha

        created_at = self.created_at.isoformat()

        observed_requests = self.observed_requests

        http_5xx_requests = self.http_5xx_requests

        http_5xx_rate: float | None
        http_5xx_rate = self.http_5xx_rate

        request_coverage: str = self.request_coverage

        observed_issue_events = self.observed_issue_events

        observed_issue_groups = self.observed_issue_groups

        new_issue_groups = self.new_issue_groups

        regressed_issue_groups = self.regressed_issue_groups

        identified_customers = self.identified_customers

        unattributed_events = self.unattributed_events

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "commit_sha": commit_sha,
                "created_at": created_at,
                "observed_requests": observed_requests,
                "http_5xx_requests": http_5xx_requests,
                "http_5xx_rate": http_5xx_rate,
                "request_coverage": request_coverage,
                "observed_issue_events": observed_issue_events,
                "observed_issue_groups": observed_issue_groups,
                "new_issue_groups": new_issue_groups,
                "regressed_issue_groups": regressed_issue_groups,
                "identified_customers": identified_customers,
                "unattributed_events": unattributed_events,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        commit_sha = d.pop("commit_sha")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        observed_requests = d.pop("observed_requests")

        http_5xx_requests = d.pop("http_5xx_requests")

        def _parse_http_5xx_rate(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        http_5xx_rate = _parse_http_5xx_rate(d.pop("http_5xx_rate"))

        request_coverage = check_issue_release_health_request_coverage(d.pop("request_coverage"))

        observed_issue_events = d.pop("observed_issue_events")

        observed_issue_groups = d.pop("observed_issue_groups")

        new_issue_groups = d.pop("new_issue_groups")

        regressed_issue_groups = d.pop("regressed_issue_groups")

        identified_customers = d.pop("identified_customers")

        unattributed_events = d.pop("unattributed_events")

        issue_release_health = cls(
            deployment_id=deployment_id,
            commit_sha=commit_sha,
            created_at=created_at,
            observed_requests=observed_requests,
            http_5xx_requests=http_5xx_requests,
            http_5xx_rate=http_5xx_rate,
            request_coverage=request_coverage,
            observed_issue_events=observed_issue_events,
            observed_issue_groups=observed_issue_groups,
            new_issue_groups=new_issue_groups,
            regressed_issue_groups=regressed_issue_groups,
            identified_customers=identified_customers,
            unattributed_events=unattributed_events,
        )

        issue_release_health.additional_properties = d
        return issue_release_health

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
