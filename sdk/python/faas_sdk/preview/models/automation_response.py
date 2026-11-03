from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_response_source import AutomationResponseSource, check_automation_response_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_test_report import AutomationTestReport
    from ..models.automation_test_suite import AutomationTestSuite
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="AutomationResponse")


@_attrs_define
class AutomationResponse:
    """Saved draft, publication, ownership and opaque revision of one automation."""

    name: str
    version: int
    """Opaque monotonically increasing revision. Zero creates the first draft. A stale value returns
    automation_version_conflict."""
    source: AutomationResponseSource
    draft: WorkflowSpec
    """A named workflow DAG submitted with a deployment. max_concurrent_runs caps active run instances for this
    workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""
    enabled: bool
    test_suite: AutomationTestSuite | Unset = UNSET
    """At most 32 uniquely named cases; sample up to 3 MiB, cases up to 24 MiB combined, suite up to 27 MiB. Data
    is explicitly persisted, never captured from real runs."""
    test_report: AutomationTestReport | Unset = UNSET
    """Retained observation, not a publishing receipt. Freshness compares definition and suite content; running
    does not advance the draft version or perform real actions."""
    failure_paused: bool | Unset = UNSET
    """Runtime failure guard blocks automatic admissions independently of configured enabled intent."""
    published: WorkflowSpec | Unset = UNSET
    """A named workflow DAG submitted with a deployment. max_concurrent_runs caps active run instances for this
    workflow; excess admitted runs remain pending until a slot opens, subject to the app plan's run quota.
    max_concurrent_actions caps active executor steps across runs of this workflow; steps wait in the scheduler
    queue while all action slots are occupied."""
    published_version: int | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        version = self.version

        source: str = self.source

        draft = self.draft.to_dict()

        enabled = self.enabled

        test_suite: dict[str, Any] | Unset = UNSET
        if not isinstance(self.test_suite, Unset):
            test_suite = self.test_suite.to_dict()

        test_report: dict[str, Any] | Unset = UNSET
        if not isinstance(self.test_report, Unset):
            test_report = self.test_report.to_dict()

        failure_paused = self.failure_paused

        published: dict[str, Any] | Unset = UNSET
        if not isinstance(self.published, Unset):
            published = self.published.to_dict()

        published_version = self.published_version

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "version": version,
                "source": source,
                "draft": draft,
                "enabled": enabled,
            }
        )
        if test_suite is not UNSET:
            field_dict["test_suite"] = test_suite
        if test_report is not UNSET:
            field_dict["test_report"] = test_report
        if failure_paused is not UNSET:
            field_dict["failure_paused"] = failure_paused
        if published is not UNSET:
            field_dict["published"] = published
        if published_version is not UNSET:
            field_dict["published_version"] = published_version
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_test_report import AutomationTestReport
        from ..models.automation_test_suite import AutomationTestSuite
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        name = d.pop("name")

        version = d.pop("version")

        source = check_automation_response_source(d.pop("source"))

        draft = WorkflowSpec.from_dict(d.pop("draft"))

        enabled = d.pop("enabled")

        _test_suite = d.pop("test_suite", UNSET)
        test_suite: AutomationTestSuite | Unset
        if isinstance(_test_suite, Unset):
            test_suite = UNSET
        else:
            test_suite = AutomationTestSuite.from_dict(_test_suite)

        _test_report = d.pop("test_report", UNSET)
        test_report: AutomationTestReport | Unset
        if isinstance(_test_report, Unset):
            test_report = UNSET
        else:
            test_report = AutomationTestReport.from_dict(_test_report)

        failure_paused = d.pop("failure_paused", UNSET)

        _published = d.pop("published", UNSET)
        published: WorkflowSpec | Unset
        if isinstance(_published, Unset):
            published = UNSET
        else:
            published = WorkflowSpec.from_dict(_published)

        published_version = d.pop("published_version", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        automation_response = cls(
            name=name,
            version=version,
            source=source,
            draft=draft,
            enabled=enabled,
            test_suite=test_suite,
            test_report=test_report,
            failure_paused=failure_paused,
            published=published,
            published_version=published_version,
            updated_at=updated_at,
        )

        return automation_response
