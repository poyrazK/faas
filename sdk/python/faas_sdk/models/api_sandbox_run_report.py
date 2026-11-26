from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_run_report_mode import APISandboxRunReportMode, check_api_sandbox_run_report_mode
from ..models.api_sandbox_run_report_status import APISandboxRunReportStatus, check_api_sandbox_run_report_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.api_sandbox_run_step import APISandboxRunStep


T = TypeVar("T", bound="APISandboxRunReport")


@_attrs_define
class APISandboxRunReport:
    """Run outcome and recorded steps with their request or delivery identifiers."""

    id: UUID
    source_deployment_id: UUID
    scenario_revision: UUID
    scenario_name: str
    mode: APISandboxRunReportMode
    status: APISandboxRunReportStatus
    steps: list[APISandboxRunStep]
    created_at: datetime.datetime
    sandbox_deployment_id: UUID | Unset = UNSET
    partner_step_count: int | Unset = UNSET
    partner_webhook: bool | Unset = UNSET
    partner_outcome_until: datetime.datetime | Unset = UNSET
    failure_code: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_deployment_id = str(self.source_deployment_id)

        scenario_revision = str(self.scenario_revision)

        scenario_name = self.scenario_name

        mode: str = self.mode

        status: str = self.status

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        created_at = self.created_at.isoformat()

        sandbox_deployment_id: str | Unset = UNSET
        if not isinstance(self.sandbox_deployment_id, Unset):
            sandbox_deployment_id = str(self.sandbox_deployment_id)

        partner_step_count = self.partner_step_count

        partner_webhook = self.partner_webhook

        partner_outcome_until: str | Unset = UNSET
        if not isinstance(self.partner_outcome_until, Unset):
            partner_outcome_until = self.partner_outcome_until.isoformat()

        failure_code = self.failure_code

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "source_deployment_id": source_deployment_id,
                "scenario_revision": scenario_revision,
                "scenario_name": scenario_name,
                "mode": mode,
                "status": status,
                "steps": steps,
                "created_at": created_at,
            }
        )
        if sandbox_deployment_id is not UNSET:
            field_dict["sandbox_deployment_id"] = sandbox_deployment_id
        if partner_step_count is not UNSET:
            field_dict["partner_step_count"] = partner_step_count
        if partner_webhook is not UNSET:
            field_dict["partner_webhook"] = partner_webhook
        if partner_outcome_until is not UNSET:
            field_dict["partner_outcome_until"] = partner_outcome_until
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.api_sandbox_run_step import APISandboxRunStep

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_deployment_id = UUID(d.pop("source_deployment_id"))

        scenario_revision = UUID(d.pop("scenario_revision"))

        scenario_name = d.pop("scenario_name")

        mode = check_api_sandbox_run_report_mode(d.pop("mode"))

        status = check_api_sandbox_run_report_status(d.pop("status"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = APISandboxRunStep.from_dict(steps_item_data)

            steps.append(steps_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _sandbox_deployment_id = d.pop("sandbox_deployment_id", UNSET)
        sandbox_deployment_id: UUID | Unset
        if isinstance(_sandbox_deployment_id, Unset):
            sandbox_deployment_id = UNSET
        else:
            sandbox_deployment_id = UUID(_sandbox_deployment_id)

        partner_step_count = d.pop("partner_step_count", UNSET)

        partner_webhook = d.pop("partner_webhook", UNSET)

        _partner_outcome_until = d.pop("partner_outcome_until", UNSET)
        partner_outcome_until: datetime.datetime | Unset
        if isinstance(_partner_outcome_until, Unset):
            partner_outcome_until = UNSET
        else:
            partner_outcome_until = datetime.datetime.fromisoformat(_partner_outcome_until)

        failure_code = d.pop("failure_code", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        api_sandbox_run_report = cls(
            id=id,
            source_deployment_id=source_deployment_id,
            scenario_revision=scenario_revision,
            scenario_name=scenario_name,
            mode=mode,
            status=status,
            steps=steps,
            created_at=created_at,
            sandbox_deployment_id=sandbox_deployment_id,
            partner_step_count=partner_step_count,
            partner_webhook=partner_webhook,
            partner_outcome_until=partner_outcome_until,
            failure_code=failure_code,
            started_at=started_at,
            finished_at=finished_at,
        )

        api_sandbox_run_report.additional_properties = d
        return api_sandbox_run_report

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
