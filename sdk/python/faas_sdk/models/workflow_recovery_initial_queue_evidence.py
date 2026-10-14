from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_initial_queue_evidence_stage import (
    WorkflowRecoveryInitialQueueEvidenceStage,
    check_workflow_recovery_initial_queue_evidence_stage,
)

if TYPE_CHECKING:
    from ..models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis


T = TypeVar("T", bound="WorkflowRecoveryInitialQueueEvidence")


@_attrs_define
class WorkflowRecoveryInitialQueueEvidence:
    """Read-only initial recovery evidence. Prepared identifies a planned generation with no submitted recovery job and is
    never repairable; submitted uses exact-generation proof from the initial evidence endpoint. Neither stage grants
    execution authority.

    """

    stage: WorkflowRecoveryInitialQueueEvidenceStage
    diagnosis: WorkflowInitialEvidenceDiagnosis
    """Consistent diagnosis of retained evidence for an initial recovery review."""
    evidence_url: str
    cli_command: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        stage: str = self.stage

        diagnosis = self.diagnosis.to_dict()

        evidence_url = self.evidence_url

        cli_command = self.cli_command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "stage": stage,
                "diagnosis": diagnosis,
                "evidence_url": evidence_url,
                "cli_command": cli_command,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis

        d = dict(src_dict)
        stage = check_workflow_recovery_initial_queue_evidence_stage(d.pop("stage"))

        diagnosis = WorkflowInitialEvidenceDiagnosis.from_dict(d.pop("diagnosis"))

        evidence_url = d.pop("evidence_url")

        cli_command = d.pop("cli_command")

        workflow_recovery_initial_queue_evidence = cls(
            stage=stage,
            diagnosis=diagnosis,
            evidence_url=evidence_url,
            cli_command=cli_command,
        )

        workflow_recovery_initial_queue_evidence.additional_properties = d
        return workflow_recovery_initial_queue_evidence

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
