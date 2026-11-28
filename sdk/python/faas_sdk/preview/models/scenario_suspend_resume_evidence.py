from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_suspend_resume_evidence_status import (
    ScenarioSuspendResumeEvidenceStatus,
    check_scenario_suspend_resume_evidence_status,
)
from ..models.scenario_suspend_resume_evidence_version import (
    ScenarioSuspendResumeEvidenceVersion,
    check_scenario_suspend_resume_evidence_version,
)

if TYPE_CHECKING:
    from ..models.scenario_suspend_resume_receipt import ScenarioSuspendResumeReceipt
    from ..models.scenario_suspend_resume_spec import ScenarioSuspendResumeSpec


T = TypeVar("T", bound="ScenarioSuspendResumeEvidence")


@_attrs_define
class ScenarioSuspendResumeEvidence:
    """Versioned lifecycle inventory. Complete receipts preserve identity while comparisons exclude timing jitter; missing
    proof remains inconclusive.

    """

    version: ScenarioSuspendResumeEvidenceVersion
    run_id: str
    spec: ScenarioSuspendResumeSpec
    """One contained in-place pause after setup or at a cooperative managed-job checkpoint. No persistent VM
    snapshot is written."""
    plan_sha256: str
    status: ScenarioSuspendResumeEvidenceStatus
    receipts: list[ScenarioSuspendResumeReceipt]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        run_id = self.run_id

        spec = self.spec.to_dict()

        plan_sha256 = self.plan_sha256

        status: str = self.status

        receipts = []
        for receipts_item_data in self.receipts:
            receipts_item = receipts_item_data.to_dict()
            receipts.append(receipts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "run_id": run_id,
                "spec": spec,
                "plan_sha256": plan_sha256,
                "status": status,
                "receipts": receipts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_suspend_resume_receipt import ScenarioSuspendResumeReceipt
        from ..models.scenario_suspend_resume_spec import ScenarioSuspendResumeSpec

        d = dict(src_dict)
        version = check_scenario_suspend_resume_evidence_version(d.pop("version"))

        run_id = d.pop("run_id")

        spec = ScenarioSuspendResumeSpec.from_dict(d.pop("spec"))

        plan_sha256 = d.pop("plan_sha256")

        status = check_scenario_suspend_resume_evidence_status(d.pop("status"))

        receipts = []
        _receipts = d.pop("receipts")
        for receipts_item_data in _receipts:
            receipts_item = ScenarioSuspendResumeReceipt.from_dict(receipts_item_data)

            receipts.append(receipts_item)

        scenario_suspend_resume_evidence = cls(
            version=version,
            run_id=run_id,
            spec=spec,
            plan_sha256=plan_sha256,
            status=status,
            receipts=receipts,
        )

        scenario_suspend_resume_evidence.additional_properties = d
        return scenario_suspend_resume_evidence

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
