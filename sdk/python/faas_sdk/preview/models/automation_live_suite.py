from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.automation_live_suite_outcome import AutomationLiveSuiteOutcome, check_automation_live_suite_outcome
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_suite_case import AutomationLiveSuiteCase
    from ..models.automation_live_suite_counts import AutomationLiveSuiteCounts


T = TypeVar("T", bound="AutomationLiveSuite")


@_attrs_define
class AutomationLiveSuite:
    """Server-derived observation of retained verification records. GET is read-only and never checks assertions or admits
    work. Missing records remain unavailable with their known IDs. Passed requires every linked case to have a current
    passing report and an unchanged saved version, published revision and deployment. Not a publication receipt.

    """

    id: UUID
    name: str
    version: int
    published_version: int
    deployment_id: UUID
    created_at: datetime.datetime
    updated_at: datetime.datetime
    case_count: int
    linked_runs: int
    outcome: AutomationLiveSuiteOutcome
    outdated: bool
    observed_at: datetime.datetime
    summary: AutomationLiveSuiteCounts
    """Current suite case counts, including unstarted and unverifiable members."""
    cases: list[AutomationLiveSuiteCase]
    managed: bool | Unset = UNSET
    """True when the suite owns durable server-managed execution and can be continued with explicit real-action
    confirmation."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        version = self.version

        published_version = self.published_version

        deployment_id = str(self.deployment_id)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        case_count = self.case_count

        linked_runs = self.linked_runs

        outcome: str = self.outcome

        outdated = self.outdated

        observed_at = self.observed_at.isoformat()

        summary = self.summary.to_dict()

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        managed = self.managed

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "version": version,
                "published_version": published_version,
                "deployment_id": deployment_id,
                "created_at": created_at,
                "updated_at": updated_at,
                "case_count": case_count,
                "linked_runs": linked_runs,
                "outcome": outcome,
                "outdated": outdated,
                "observed_at": observed_at,
                "summary": summary,
                "cases": cases,
            }
        )
        if managed is not UNSET:
            field_dict["managed"] = managed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_suite_case import AutomationLiveSuiteCase
        from ..models.automation_live_suite_counts import AutomationLiveSuiteCounts

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        version = d.pop("version")

        published_version = d.pop("published_version")

        deployment_id = UUID(d.pop("deployment_id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        case_count = d.pop("case_count")

        linked_runs = d.pop("linked_runs")

        outcome = check_automation_live_suite_outcome(d.pop("outcome"))

        outdated = d.pop("outdated")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        summary = AutomationLiveSuiteCounts.from_dict(d.pop("summary"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = AutomationLiveSuiteCase.from_dict(cases_item_data)

            cases.append(cases_item)

        managed = d.pop("managed", UNSET)

        automation_live_suite = cls(
            id=id,
            name=name,
            version=version,
            published_version=published_version,
            deployment_id=deployment_id,
            created_at=created_at,
            updated_at=updated_at,
            case_count=case_count,
            linked_runs=linked_runs,
            outcome=outcome,
            outdated=outdated,
            observed_at=observed_at,
            summary=summary,
            cases=cases,
            managed=managed,
        )

        return automation_live_suite
