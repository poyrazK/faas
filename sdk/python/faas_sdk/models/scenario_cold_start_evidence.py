from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_cold_start_evidence_status import (
    ScenarioColdStartEvidenceStatus,
    check_scenario_cold_start_evidence_status,
)
from ..models.scenario_cold_start_evidence_version import (
    ScenarioColdStartEvidenceVersion,
    check_scenario_cold_start_evidence_version,
)

if TYPE_CHECKING:
    from ..models.scenario_cold_preparation import ScenarioColdPreparation
    from ..models.scenario_cold_start_spec import ScenarioColdStartSpec


T = TypeVar("T", bound="ScenarioColdStartEvidence")


@_attrs_define
class ScenarioColdStartEvidence:
    """Sealed cold-start inventory with immutable recipe identity and independent host receipts. Raw timing jitter and
    runtime IDs are excluded from behavioral equality.

    """

    version: ScenarioColdStartEvidenceVersion
    run_id: str
    spec: ScenarioColdStartSpec
    """Versioned contained cold-start budget for every declared workload. Requires an operator-qualified cold-start
    fleet and excludes worker termination."""
    plan_sha256: str
    status: ScenarioColdStartEvidenceStatus
    preparations: list[ScenarioColdPreparation]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        run_id = self.run_id

        spec = self.spec.to_dict()

        plan_sha256 = self.plan_sha256

        status: str = self.status

        preparations = []
        for preparations_item_data in self.preparations:
            preparations_item = preparations_item_data.to_dict()
            preparations.append(preparations_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "run_id": run_id,
                "spec": spec,
                "plan_sha256": plan_sha256,
                "status": status,
                "preparations": preparations,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_cold_preparation import ScenarioColdPreparation
        from ..models.scenario_cold_start_spec import ScenarioColdStartSpec

        d = dict(src_dict)
        version = check_scenario_cold_start_evidence_version(d.pop("version"))

        run_id = d.pop("run_id")

        spec = ScenarioColdStartSpec.from_dict(d.pop("spec"))

        plan_sha256 = d.pop("plan_sha256")

        status = check_scenario_cold_start_evidence_status(d.pop("status"))

        preparations = []
        _preparations = d.pop("preparations")
        for preparations_item_data in _preparations:
            preparations_item = ScenarioColdPreparation.from_dict(preparations_item_data)

            preparations.append(preparations_item)

        scenario_cold_start_evidence = cls(
            version=version,
            run_id=run_id,
            spec=spec,
            plan_sha256=plan_sha256,
            status=status,
            preparations=preparations,
        )

        scenario_cold_start_evidence.additional_properties = d
        return scenario_cold_start_evidence

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
