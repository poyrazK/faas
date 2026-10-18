from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_managed_work_evidence_status import (
    ScenarioManagedWorkEvidenceStatus,
    check_scenario_managed_work_evidence_status,
)
from ..models.scenario_managed_work_evidence_version import (
    ScenarioManagedWorkEvidenceVersion,
    check_scenario_managed_work_evidence_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_managed_work_binding import ScenarioManagedWorkBinding
    from ..models.scenario_managed_work_job import ScenarioManagedWorkJob
    from ..models.scenario_managed_work_plan import ScenarioManagedWorkPlan
    from ..models.scenario_worker_fault import ScenarioWorkerFault


T = TypeVar("T", bound="ScenarioManagedWorkEvidence")


@_attrs_define
class ScenarioManagedWorkEvidence:
    """Bounded participant inventory and managed async completion proof. A complete result requires worker execution,
    successful receipts and closed admission.

    """

    version: ScenarioManagedWorkEvidenceVersion
    run_id: str
    plan_sha256: str
    status: ScenarioManagedWorkEvidenceStatus
    closing: bool
    plan: ScenarioManagedWorkPlan
    """Explicit participants and fixture permissions for managed async-route rehearsals. Requires contained
    commands and simulation; rollout is operator gated."""
    bindings: list[ScenarioManagedWorkBinding]
    jobs: list[ScenarioManagedWorkJob]
    worker_fault: ScenarioWorkerFault | Unset = UNSET
    """Platform-owned checkpoint, collector and VM termination identity. Complete evidence requires a terminated
    receipt and independently journalled recovery on a different VM."""
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        run_id = self.run_id

        plan_sha256 = self.plan_sha256

        status: str = self.status

        closing = self.closing

        plan = self.plan.to_dict()

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        jobs = []
        for jobs_item_data in self.jobs:
            jobs_item = jobs_item_data.to_dict()
            jobs.append(jobs_item)

        worker_fault: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker_fault, Unset):
            worker_fault = self.worker_fault.to_dict()

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "run_id": run_id,
                "plan_sha256": plan_sha256,
                "status": status,
                "closing": closing,
                "plan": plan,
                "bindings": bindings,
                "jobs": jobs,
            }
        )
        if worker_fault is not UNSET:
            field_dict["worker_fault"] = worker_fault
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_managed_work_binding import ScenarioManagedWorkBinding
        from ..models.scenario_managed_work_job import ScenarioManagedWorkJob
        from ..models.scenario_managed_work_plan import ScenarioManagedWorkPlan
        from ..models.scenario_worker_fault import ScenarioWorkerFault

        d = dict(src_dict)
        version = check_scenario_managed_work_evidence_version(d.pop("version"))

        run_id = d.pop("run_id")

        plan_sha256 = d.pop("plan_sha256")

        status = check_scenario_managed_work_evidence_status(d.pop("status"))

        closing = d.pop("closing")

        plan = ScenarioManagedWorkPlan.from_dict(d.pop("plan"))

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = ScenarioManagedWorkBinding.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        jobs = []
        _jobs = d.pop("jobs")
        for jobs_item_data in _jobs:
            jobs_item = ScenarioManagedWorkJob.from_dict(jobs_item_data)

            jobs.append(jobs_item)

        _worker_fault = d.pop("worker_fault", UNSET)
        worker_fault: ScenarioWorkerFault | Unset
        if isinstance(_worker_fault, Unset):
            worker_fault = UNSET
        else:
            worker_fault = ScenarioWorkerFault.from_dict(_worker_fault)

        reason = d.pop("reason", UNSET)

        scenario_managed_work_evidence = cls(
            version=version,
            run_id=run_id,
            plan_sha256=plan_sha256,
            status=status,
            closing=closing,
            plan=plan,
            bindings=bindings,
            jobs=jobs,
            worker_fault=worker_fault,
            reason=reason,
        )

        scenario_managed_work_evidence.additional_properties = d
        return scenario_managed_work_evidence

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
