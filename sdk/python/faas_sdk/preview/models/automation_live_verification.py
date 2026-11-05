from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.automation_live_report import AutomationLiveReport


T = TypeVar("T", bound="AutomationLiveVerification")


@_attrs_define
class AutomationLiveVerification:
    """Safe provenance and status for one explicitly authorized live verification run."""

    run_id: UUID
    case_name: str
    deployment_id: str
    inputs_hash: str
    run_status: str
    version: int
    published_version: int
    created_at: datetime.datetime
    outdated: bool
    """True when the saved case, published revision, or current default deployment differs from the frozen target."""
    report_current: bool
    """True when the report still matches this run status, resume count and update time; also require
    outdated=false for current-target evidence."""
    report: AutomationLiveReport | Unset = UNSET
    """Payload-free verdict for the frozen live assertions at one terminal run state."""

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        case_name = self.case_name

        deployment_id = self.deployment_id

        inputs_hash = self.inputs_hash

        run_status = self.run_status

        version = self.version

        published_version = self.published_version

        created_at = self.created_at.isoformat()

        outdated = self.outdated

        report_current = self.report_current

        report: dict[str, Any] | Unset = UNSET
        if not isinstance(self.report, Unset):
            report = self.report.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_id": run_id,
                "case_name": case_name,
                "deployment_id": deployment_id,
                "inputs_hash": inputs_hash,
                "run_status": run_status,
                "version": version,
                "published_version": published_version,
                "created_at": created_at,
                "outdated": outdated,
                "report_current": report_current,
            }
        )
        if report is not UNSET:
            field_dict["report"] = report

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_report import AutomationLiveReport

        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        case_name = d.pop("case_name")

        deployment_id = d.pop("deployment_id")

        inputs_hash = d.pop("inputs_hash")

        run_status = d.pop("run_status")

        version = d.pop("version")

        published_version = d.pop("published_version")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        outdated = d.pop("outdated")

        report_current = d.pop("report_current")

        _report = d.pop("report", UNSET)
        report: AutomationLiveReport | Unset
        if isinstance(_report, Unset):
            report = UNSET
        else:
            report = AutomationLiveReport.from_dict(_report)

        automation_live_verification = cls(
            run_id=run_id,
            case_name=case_name,
            deployment_id=deployment_id,
            inputs_hash=inputs_hash,
            run_status=run_status,
            version=version,
            published_version=published_version,
            created_at=created_at,
            outdated=outdated,
            report_current=report_current,
            report=report,
        )

        return automation_live_verification
