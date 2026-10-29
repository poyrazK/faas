from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence


T = TypeVar("T", bound="OperationWorkflowRuleVersionOverlap")


@_attrs_define
class OperationWorkflowRuleVersionOverlap:
    """Bounded observed multi-version example in stable workflow identity order. Up to 16 earliest-per-version decision
    reports appear in revision order; ties do not imply business ordering. Missing history may hide other versions or
    earlier terminal reports. Evidence links explain observed membership and do not establish that a policy was
    replaced.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    instance_id: str
    operation_id: UUID
    history_complete: bool
    eligible: bool
    evidence_truncated: bool
    evidence: list[OperationWorkflowDecisionOutcomeEvidence]
    platform_tenant_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        instance_id = self.instance_id

        operation_id = str(self.operation_id)

        history_complete = self.history_complete

        eligible = self.eligible

        evidence_truncated = self.evidence_truncated

        evidence = []
        for evidence_item_data in self.evidence:
            evidence_item = evidence_item_data.to_dict()
            evidence.append(evidence_item)

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "instance_id": instance_id,
                "operation_id": operation_id,
                "history_complete": history_complete,
                "eligible": eligible,
                "evidence_truncated": evidence_truncated,
                "evidence": evidence,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_decision_outcome_evidence import OperationWorkflowDecisionOutcomeEvidence

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        instance_id = d.pop("instance_id")

        operation_id = UUID(d.pop("operation_id"))

        history_complete = d.pop("history_complete")

        eligible = d.pop("eligible")

        evidence_truncated = d.pop("evidence_truncated")

        evidence = []
        _evidence = d.pop("evidence")
        for evidence_item_data in _evidence:
            evidence_item = OperationWorkflowDecisionOutcomeEvidence.from_dict(evidence_item_data)

            evidence.append(evidence_item)

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        operation_workflow_rule_version_overlap = cls(
            subject=subject,
            instance_id=instance_id,
            operation_id=operation_id,
            history_complete=history_complete,
            eligible=eligible,
            evidence_truncated=evidence_truncated,
            evidence=evidence,
            platform_tenant_id=platform_tenant_id,
        )

        return operation_workflow_rule_version_overlap
