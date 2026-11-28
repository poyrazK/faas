from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_playbook_match_kind import (
    OperationWorkflowPlaybookMatchKind,
    check_operation_workflow_playbook_match_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_playbook_evidence_requirement import OperationWorkflowPlaybookEvidenceRequirement
    from ..models.operation_workflow_playbook_outcome_acceptance_policy import (
        OperationWorkflowPlaybookOutcomeAcceptancePolicy,
    )
    from ..models.operation_workflow_playbook_outcome_deadline_policy import (
        OperationWorkflowPlaybookOutcomeDeadlinePolicy,
    )
    from ..models.operation_workflow_playbook_outcome_guidance import OperationWorkflowPlaybookOutcomeGuidance


T = TypeVar("T", bound="OperationWorkflowPlaybook")


@_attrs_define
class OperationWorkflowPlaybook:
    """OperationWorkflowPlaybook. Application-owned advisory guidance; evidence requirements are human checklists and links
    are never executed by Gregale.

    """

    id: str
    """OperationWorkflowPlaybook.id. Unique bounded lowercase identifier within the workflow contract."""
    version: int
    """OperationWorkflowPlaybook.version. Explicit playbook revision, pinned alongside the workflow contract
    version."""
    match_kind: OperationWorkflowPlaybookMatchKind
    """OperationWorkflowPlaybook.match_kind. Exact observed fact category."""
    match_code: str
    """OperationWorkflowPlaybook.match_code. Exact blocker code, milestone, dependency workflow, verification
    blocker code or readiness reason code."""
    summary: str
    """OperationWorkflowPlaybook.summary. Application-authored guidance without control characters or surrounding
    whitespace."""
    outcome_guidance: list[OperationWorkflowPlaybookOutcomeGuidance] | Unset = UNSET
    """OperationWorkflowPlaybook.outcome_guidance. Optional pinned descriptions and suggested follow-up for the
    expected outcome and explicitly allowed alternatives; unique by outcome code."""
    outcome_acceptance: OperationWorkflowPlaybookOutcomeAcceptancePolicy | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeAcceptancePolicy. Explicit business acceptance rules pinned with the
    playbook. Requires an expected outcome; an empty list permits no alternatives. Omission preserves legacy
    reviewer acceptance."""
    outcome_deadline: OperationWorkflowPlaybookOutcomeDeadlinePolicy | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy. Application-defined business attention policy. Requires an
    expected terminal outcome; all durations are whole seconds and the owner is a coordination label, not an
    external destination."""
    expected_outcome_code: str | Unset = UNSET
    """OperationWorkflowPlaybook.expected_outcome_code. Optional expected application terminal outcome, frozen with
    guidance; does not infer causation or close coordination."""
    owner: str | Unset = UNSET
    """OperationWorkflowPlaybook.owner. Suggested responsible team; does not assign or override coordination
    ownership."""
    evidence_requirements: list[OperationWorkflowPlaybookEvidenceRequirement] | Unset = UNSET
    """OperationWorkflowPlaybook.evidence_requirements. Optional unique live selectors for declared checklist
    labels. Frozen with guidance; human reviews remain independent."""
    required_evidence: list[str] | Unset = UNSET
    """OperationWorkflowPlaybook.required_evidence. Human checklist labels with optional live application-evidence
    selectors."""
    runbook_url: str | Unset = UNSET
    """OperationWorkflowPlaybook.runbook_url. Absolute HTTPS runbook link without user credentials; never fetched
    automatically."""
    action_url: str | Unset = UNSET
    """OperationWorkflowPlaybook.action_url. Absolute HTTPS application action link without user credentials;
    application handles authorization and execution."""

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        version = self.version

        match_kind: str = self.match_kind

        match_code = self.match_code

        summary = self.summary

        outcome_guidance: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.outcome_guidance, Unset):
            outcome_guidance = []
            for outcome_guidance_item_data in self.outcome_guidance:
                outcome_guidance_item = outcome_guidance_item_data.to_dict()
                outcome_guidance.append(outcome_guidance_item)

        outcome_acceptance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome_acceptance, Unset):
            outcome_acceptance = self.outcome_acceptance.to_dict()

        outcome_deadline: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome_deadline, Unset):
            outcome_deadline = self.outcome_deadline.to_dict()

        expected_outcome_code = self.expected_outcome_code

        owner = self.owner

        evidence_requirements: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence_requirements, Unset):
            evidence_requirements = []
            for evidence_requirements_item_data in self.evidence_requirements:
                evidence_requirements_item = evidence_requirements_item_data.to_dict()
                evidence_requirements.append(evidence_requirements_item)

        required_evidence: list[str] | Unset = UNSET
        if not isinstance(self.required_evidence, Unset):
            required_evidence = self.required_evidence

        runbook_url = self.runbook_url

        action_url = self.action_url

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "version": version,
                "match_kind": match_kind,
                "match_code": match_code,
                "summary": summary,
            }
        )
        if outcome_guidance is not UNSET:
            field_dict["outcome_guidance"] = outcome_guidance
        if outcome_acceptance is not UNSET:
            field_dict["outcome_acceptance"] = outcome_acceptance
        if outcome_deadline is not UNSET:
            field_dict["outcome_deadline"] = outcome_deadline
        if expected_outcome_code is not UNSET:
            field_dict["expected_outcome_code"] = expected_outcome_code
        if owner is not UNSET:
            field_dict["owner"] = owner
        if evidence_requirements is not UNSET:
            field_dict["evidence_requirements"] = evidence_requirements
        if required_evidence is not UNSET:
            field_dict["required_evidence"] = required_evidence
        if runbook_url is not UNSET:
            field_dict["runbook_url"] = runbook_url
        if action_url is not UNSET:
            field_dict["action_url"] = action_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_playbook_evidence_requirement import (
            OperationWorkflowPlaybookEvidenceRequirement,
        )
        from ..models.operation_workflow_playbook_outcome_acceptance_policy import (
            OperationWorkflowPlaybookOutcomeAcceptancePolicy,
        )
        from ..models.operation_workflow_playbook_outcome_deadline_policy import (
            OperationWorkflowPlaybookOutcomeDeadlinePolicy,
        )
        from ..models.operation_workflow_playbook_outcome_guidance import OperationWorkflowPlaybookOutcomeGuidance

        d = dict(src_dict)
        id = d.pop("id")

        version = d.pop("version")

        match_kind = check_operation_workflow_playbook_match_kind(d.pop("match_kind"))

        match_code = d.pop("match_code")

        summary = d.pop("summary")

        _outcome_guidance = d.pop("outcome_guidance", UNSET)
        outcome_guidance: list[OperationWorkflowPlaybookOutcomeGuidance] | Unset = UNSET
        if _outcome_guidance is not UNSET:
            outcome_guidance = []
            for outcome_guidance_item_data in _outcome_guidance:
                outcome_guidance_item = OperationWorkflowPlaybookOutcomeGuidance.from_dict(outcome_guidance_item_data)

                outcome_guidance.append(outcome_guidance_item)

        _outcome_acceptance = d.pop("outcome_acceptance", UNSET)
        outcome_acceptance: OperationWorkflowPlaybookOutcomeAcceptancePolicy | Unset
        if isinstance(_outcome_acceptance, Unset):
            outcome_acceptance = UNSET
        else:
            outcome_acceptance = OperationWorkflowPlaybookOutcomeAcceptancePolicy.from_dict(_outcome_acceptance)

        _outcome_deadline = d.pop("outcome_deadline", UNSET)
        outcome_deadline: OperationWorkflowPlaybookOutcomeDeadlinePolicy | Unset
        if isinstance(_outcome_deadline, Unset):
            outcome_deadline = UNSET
        else:
            outcome_deadline = OperationWorkflowPlaybookOutcomeDeadlinePolicy.from_dict(_outcome_deadline)

        expected_outcome_code = d.pop("expected_outcome_code", UNSET)

        owner = d.pop("owner", UNSET)

        _evidence_requirements = d.pop("evidence_requirements", UNSET)
        evidence_requirements: list[OperationWorkflowPlaybookEvidenceRequirement] | Unset = UNSET
        if _evidence_requirements is not UNSET:
            evidence_requirements = []
            for evidence_requirements_item_data in _evidence_requirements:
                evidence_requirements_item = OperationWorkflowPlaybookEvidenceRequirement.from_dict(
                    evidence_requirements_item_data
                )

                evidence_requirements.append(evidence_requirements_item)

        required_evidence = cast(list[str], d.pop("required_evidence", UNSET))

        runbook_url = d.pop("runbook_url", UNSET)

        action_url = d.pop("action_url", UNSET)

        operation_workflow_playbook = cls(
            id=id,
            version=version,
            match_kind=match_kind,
            match_code=match_code,
            summary=summary,
            outcome_guidance=outcome_guidance,
            outcome_acceptance=outcome_acceptance,
            outcome_deadline=outcome_deadline,
            expected_outcome_code=expected_outcome_code,
            owner=owner,
            evidence_requirements=evidence_requirements,
            required_evidence=required_evidence,
            runbook_url=runbook_url,
            action_url=action_url,
        )

        return operation_workflow_playbook
