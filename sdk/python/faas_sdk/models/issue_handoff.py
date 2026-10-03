from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_handoff_schema_version import IssueHandoffSchemaVersion, check_issue_handoff_schema_version
from ..models.issue_handoff_type import IssueHandoffType, check_issue_handoff_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue import Issue
    from ..models.issue_activity import IssueActivity
    from ..models.issue_event import IssueEvent
    from ..models.issue_handoff_evidence import IssueHandoffEvidence
    from ..models.issue_handoff_request import IssueHandoffRequest
    from ..models.issue_impact import IssueImpact
    from ..models.issue_release import IssueRelease


T = TypeVar("T", bound="IssueHandoff")


@_attrs_define
class IssueHandoff:
    """Opt-in issue.handoff webhook data, captured atomically with a created,
    regressed, reopened, or impact_threshold_reached transition. Retries
    preserve the snapshot. The 128 KiB packet includes retained sanitized
    evidence; gaps explicitly describe unavailable or truncated evidence.
    The legacy JSON envelope carries this under payload.data; CloudEvents
    carries it under data. Receivers must authenticate separately to follow
    the evidence API paths. Empty webhook filters do not select this event.

    """

    schema_version: IssueHandoffSchemaVersion
    id: UUID
    """Stable handoff identity shared by every recipient; distinct from transition and delivery IDs."""
    type_: IssueHandoffType
    generated_at: datetime.datetime
    issue: Issue
    """Durable failure group across releases in one app and environment."""
    transition: IssueActivity
    """An audited issue lifecycle or ownership transition."""
    impact: IssueImpact
    """Observed retained customer impact within an explicit time window; unknown identity remains unattributed."""
    evidence: IssueHandoffEvidence
    """Relative API paths requiring the receiver's own authenticated read access; reporting tokens and webhook
    signatures grant no access."""
    gaps: list[str]
    sample: IssueEvent | Unset = UNSET
    """Structured exception evidence; credential-derived account and deployment identity cannot be overridden."""
    release: IssueRelease | Unset = UNSET
    """Immutable deployment metadata and aggregate observations for one issue."""
    request: IssueHandoffRequest | Unset = UNSET
    """Uniquely correlated singleton telemetry within the account's plan retention, with at most eight slowest
    sanitized spans. No arbitrary attributes or SQL statements are forwarded."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        schema_version: int = self.schema_version

        id = str(self.id)

        type_: str = self.type_

        generated_at = self.generated_at.isoformat()

        issue = self.issue.to_dict()

        transition = self.transition.to_dict()

        impact = self.impact.to_dict()

        evidence = self.evidence.to_dict()

        gaps = self.gaps

        sample: dict[str, Any] | Unset = UNSET
        if not isinstance(self.sample, Unset):
            sample = self.sample.to_dict()

        release: dict[str, Any] | Unset = UNSET
        if not isinstance(self.release, Unset):
            release = self.release.to_dict()

        request: dict[str, Any] | Unset = UNSET
        if not isinstance(self.request, Unset):
            request = self.request.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "schema_version": schema_version,
                "id": id,
                "type": type_,
                "generated_at": generated_at,
                "issue": issue,
                "transition": transition,
                "impact": impact,
                "evidence": evidence,
                "gaps": gaps,
            }
        )
        if sample is not UNSET:
            field_dict["sample"] = sample
        if release is not UNSET:
            field_dict["release"] = release
        if request is not UNSET:
            field_dict["request"] = request

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue import Issue
        from ..models.issue_activity import IssueActivity
        from ..models.issue_event import IssueEvent
        from ..models.issue_handoff_evidence import IssueHandoffEvidence
        from ..models.issue_handoff_request import IssueHandoffRequest
        from ..models.issue_impact import IssueImpact
        from ..models.issue_release import IssueRelease

        d = dict(src_dict)
        schema_version = check_issue_handoff_schema_version(d.pop("schema_version"))

        id = UUID(d.pop("id"))

        type_ = check_issue_handoff_type(d.pop("type"))

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        issue = Issue.from_dict(d.pop("issue"))

        transition = IssueActivity.from_dict(d.pop("transition"))

        impact = IssueImpact.from_dict(d.pop("impact"))

        evidence = IssueHandoffEvidence.from_dict(d.pop("evidence"))

        gaps = cast(list[str], d.pop("gaps"))

        _sample = d.pop("sample", UNSET)
        sample: IssueEvent | Unset
        if isinstance(_sample, Unset):
            sample = UNSET
        else:
            sample = IssueEvent.from_dict(_sample)

        _release = d.pop("release", UNSET)
        release: IssueRelease | Unset
        if isinstance(_release, Unset):
            release = UNSET
        else:
            release = IssueRelease.from_dict(_release)

        _request = d.pop("request", UNSET)
        request: IssueHandoffRequest | Unset
        if isinstance(_request, Unset):
            request = UNSET
        else:
            request = IssueHandoffRequest.from_dict(_request)

        issue_handoff = cls(
            schema_version=schema_version,
            id=id,
            type_=type_,
            generated_at=generated_at,
            issue=issue,
            transition=transition,
            impact=impact,
            evidence=evidence,
            gaps=gaps,
            sample=sample,
            release=release,
            request=request,
        )

        issue_handoff.additional_properties = d
        return issue_handoff

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
