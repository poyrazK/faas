from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_workflow_case_guidance_status import (
    OperationWorkflowCaseGuidanceStatus,
    check_operation_workflow_case_guidance_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_case_action import OperationWorkflowCaseAction
    from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
    from ..models.operation_workflow_case_obligation_history_item import OperationWorkflowCaseObligationHistoryItem
    from ..models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
    from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
    from ..models.operation_workflow_obligation_assessment import OperationWorkflowObligationAssessment
    from ..models.operation_workflow_obligation_context import OperationWorkflowObligationContext
    from ..models.operation_workflow_playbook_guidance import OperationWorkflowPlaybookGuidance
    from ..models.operation_workflow_playbook_progress import OperationWorkflowPlaybookProgress
    from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp


T = TypeVar("T", bound="OperationWorkflowCase")


@_attrs_define
class OperationWorkflowCase:
    """OperationWorkflowCase. Current evidence and advisory coordination for an exact scoped business workflow."""

    obligation_assessments: list[OperationWorkflowObligationAssessment]
    obligation_history: list[OperationWorkflowCaseObligationHistoryItem]
    obligation_history_count: int
    """Matching obligation groups found in this bounded retained source scan."""
    obligation_history_truncated: bool
    """Known receipt pruning or response caps limited this history view."""
    obligation_history_limited: bool
    """Deleted sources and older receipts without case attribution are unavailable; missing events never establish
    completion."""
    obligation_history_checked_at: datetime.datetime
    next_actions: list[OperationWorkflowCaseNextAction]
    """Current obligations and suggestions from retained source records, matched to the exact case before follow-up
    page truncation. Ordered overdue first, then dated work by due time. Historical delivery snapshots do not create
    work."""
    next_action_count: int
    """Number of retained matching action rows before the 100-row cap. One source can have several obligations;
    this is not a workflow or global task count."""
    next_actions_truncated: bool
    """Additional retained actions were omitted from the bounded response."""
    playbook_progress: list[OperationWorkflowPlaybookProgress]
    """OperationWorkflowCase.playbook_progress. Durable pinned checklists for this exact case, retained
    independently of inboxes and current guidance; at most 100."""
    guidance: list[OperationWorkflowPlaybookGuidance]
    """OperationWorkflowCase.guidance. Matching application playbooks from the definition that reported current
    fresh nonterminal evidence."""
    guidance_status: OperationWorkflowCaseGuidanceStatus
    """OperationWorkflowCase.guidance_status. Availability of exact reporting-definition playbooks, independent of
    matches and readiness."""
    guidance_truncated: bool
    """OperationWorkflowCase.guidance_truncated. Matching guidance or the evaluated transition evidence was
    truncated."""
    status: str
    """OperationWorkflowCase.status. Unknown, stale, terminal_reported, no_transition, blocked or ready. Terminal
    state alone is not success."""
    summary: str
    """OperationWorkflowCase.summary. Current evidence interpretation with application authority boundaries."""
    follow_ups: list[OperationWorkflowReadinessFollowUp]
    """OperationWorkflowCase.follow_ups. Matching open scheduled follow-ups, ordered by due clock; independent of
    application readiness."""
    follow_up_count: int
    """OperationWorkflowCase.follow_up_count. Total matching retained open scheduled follow-ups before overview
    truncation."""
    overdue_follow_up_count: int
    """OperationWorkflowCase.overdue_follow_up_count. Matching retained follow-ups due by coordination check time."""
    follow_ups_truncated: bool
    """OperationWorkflowCase.follow_ups_truncated. Only the first 100 matching follow-ups are included."""
    actions: list[OperationWorkflowCaseAction]
    """OperationWorkflowCase.actions. At most 100 evidence-based suggestions, with stale/missing evidence requiring
    a refresh."""
    actions_truncated: bool
    """OperationWorkflowCase.actions_truncated. Additional suggestions exceeded the bounded overview."""
    timeline: OperationWorkflowCoordinationTimeline
    """OperationWorkflowCoordinationTimeline. Retained inbox coordination projection metadata."""
    evidence_checked_at: datetime.datetime
    """OperationWorkflowCase.evidence_checked_at. Server observation clock after the application projection read."""
    coordination_checked_at: datetime.datetime
    """OperationWorkflowCase.coordination_checked_at. Server clock used to classify follow-up overdue status."""
    evaluated_at: datetime.datetime
    """OperationWorkflowCase.evaluated_at. Server completion clock for the independent live reads."""
    limitations: list[str]
    """OperationWorkflowCase.limitations. Explicit read consistency, retention and application authority
    boundaries."""
    obligation_context: OperationWorkflowObligationContext | Unset = UNSET
    """Fresh exact-case transition context shared by obligation assessments. Detailed declared requirements and
    dependency identities remain in instance.readiness, avoiding duplicated evidence payloads. Stale or absent
    reports never produce a positive transition verdict; a satisfied edge does not grant application execution or
    bind a free-form plan."""
    instance: OperationWorkflowInstanceSnapshot | Unset = UNSET
    """Grouped view of the selected contract's declared steps and allowed transitions, current explicit state, and
    transition-history page for one workflow instance. Allowed transitions are contract edges by target Operation;
    the application still checks its business row and authorization before using one. Page-scoped facts follow the
    milestone cursor; retention-wide step summaries cover all matching facts still retained under the normal
    Operation retention rules."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        obligation_assessments = []
        for obligation_assessments_item_data in self.obligation_assessments:
            obligation_assessments_item = obligation_assessments_item_data.to_dict()
            obligation_assessments.append(obligation_assessments_item)

        obligation_history = []
        for obligation_history_item_data in self.obligation_history:
            obligation_history_item = obligation_history_item_data.to_dict()
            obligation_history.append(obligation_history_item)

        obligation_history_count = self.obligation_history_count

        obligation_history_truncated = self.obligation_history_truncated

        obligation_history_limited = self.obligation_history_limited

        obligation_history_checked_at = self.obligation_history_checked_at.isoformat()

        next_actions = []
        for next_actions_item_data in self.next_actions:
            next_actions_item = next_actions_item_data.to_dict()
            next_actions.append(next_actions_item)

        next_action_count = self.next_action_count

        next_actions_truncated = self.next_actions_truncated

        playbook_progress = []
        for playbook_progress_item_data in self.playbook_progress:
            playbook_progress_item = playbook_progress_item_data.to_dict()
            playbook_progress.append(playbook_progress_item)

        guidance = []
        for guidance_item_data in self.guidance:
            guidance_item = guidance_item_data.to_dict()
            guidance.append(guidance_item)

        guidance_status: str = self.guidance_status

        guidance_truncated = self.guidance_truncated

        status = self.status

        summary = self.summary

        follow_ups = []
        for follow_ups_item_data in self.follow_ups:
            follow_ups_item = follow_ups_item_data.to_dict()
            follow_ups.append(follow_ups_item)

        follow_up_count = self.follow_up_count

        overdue_follow_up_count = self.overdue_follow_up_count

        follow_ups_truncated = self.follow_ups_truncated

        actions = []
        for actions_item_data in self.actions:
            actions_item = actions_item_data.to_dict()
            actions.append(actions_item)

        actions_truncated = self.actions_truncated

        timeline = self.timeline.to_dict()

        evidence_checked_at = self.evidence_checked_at.isoformat()

        coordination_checked_at = self.coordination_checked_at.isoformat()

        evaluated_at = self.evaluated_at.isoformat()

        limitations = self.limitations

        obligation_context: dict[str, Any] | Unset = UNSET
        if not isinstance(self.obligation_context, Unset):
            obligation_context = self.obligation_context.to_dict()

        instance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.instance, Unset):
            instance = self.instance.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "obligation_assessments": obligation_assessments,
                "obligation_history": obligation_history,
                "obligation_history_count": obligation_history_count,
                "obligation_history_truncated": obligation_history_truncated,
                "obligation_history_limited": obligation_history_limited,
                "obligation_history_checked_at": obligation_history_checked_at,
                "next_actions": next_actions,
                "next_action_count": next_action_count,
                "next_actions_truncated": next_actions_truncated,
                "playbook_progress": playbook_progress,
                "guidance": guidance,
                "guidance_status": guidance_status,
                "guidance_truncated": guidance_truncated,
                "status": status,
                "summary": summary,
                "follow_ups": follow_ups,
                "follow_up_count": follow_up_count,
                "overdue_follow_up_count": overdue_follow_up_count,
                "follow_ups_truncated": follow_ups_truncated,
                "actions": actions,
                "actions_truncated": actions_truncated,
                "timeline": timeline,
                "evidence_checked_at": evidence_checked_at,
                "coordination_checked_at": coordination_checked_at,
                "evaluated_at": evaluated_at,
                "limitations": limitations,
            }
        )
        if obligation_context is not UNSET:
            field_dict["obligation_context"] = obligation_context
        if instance is not UNSET:
            field_dict["instance"] = instance

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_case_action import OperationWorkflowCaseAction
        from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
        from ..models.operation_workflow_case_obligation_history_item import OperationWorkflowCaseObligationHistoryItem
        from ..models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
        from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
        from ..models.operation_workflow_obligation_assessment import OperationWorkflowObligationAssessment
        from ..models.operation_workflow_obligation_context import OperationWorkflowObligationContext
        from ..models.operation_workflow_playbook_guidance import OperationWorkflowPlaybookGuidance
        from ..models.operation_workflow_playbook_progress import OperationWorkflowPlaybookProgress
        from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp

        d = dict(src_dict)
        obligation_assessments = []
        _obligation_assessments = d.pop("obligation_assessments")
        for obligation_assessments_item_data in _obligation_assessments:
            obligation_assessments_item = OperationWorkflowObligationAssessment.from_dict(
                obligation_assessments_item_data
            )

            obligation_assessments.append(obligation_assessments_item)

        obligation_history = []
        _obligation_history = d.pop("obligation_history")
        for obligation_history_item_data in _obligation_history:
            obligation_history_item = OperationWorkflowCaseObligationHistoryItem.from_dict(obligation_history_item_data)

            obligation_history.append(obligation_history_item)

        obligation_history_count = d.pop("obligation_history_count")

        obligation_history_truncated = d.pop("obligation_history_truncated")

        obligation_history_limited = d.pop("obligation_history_limited")

        obligation_history_checked_at = datetime.datetime.fromisoformat(d.pop("obligation_history_checked_at"))

        next_actions = []
        _next_actions = d.pop("next_actions")
        for next_actions_item_data in _next_actions:
            next_actions_item = OperationWorkflowCaseNextAction.from_dict(next_actions_item_data)

            next_actions.append(next_actions_item)

        next_action_count = d.pop("next_action_count")

        next_actions_truncated = d.pop("next_actions_truncated")

        playbook_progress = []
        _playbook_progress = d.pop("playbook_progress")
        for playbook_progress_item_data in _playbook_progress:
            playbook_progress_item = OperationWorkflowPlaybookProgress.from_dict(playbook_progress_item_data)

            playbook_progress.append(playbook_progress_item)

        guidance = []
        _guidance = d.pop("guidance")
        for guidance_item_data in _guidance:
            guidance_item = OperationWorkflowPlaybookGuidance.from_dict(guidance_item_data)

            guidance.append(guidance_item)

        guidance_status = check_operation_workflow_case_guidance_status(d.pop("guidance_status"))

        guidance_truncated = d.pop("guidance_truncated")

        status = d.pop("status")

        summary = d.pop("summary")

        follow_ups = []
        _follow_ups = d.pop("follow_ups")
        for follow_ups_item_data in _follow_ups:
            follow_ups_item = OperationWorkflowReadinessFollowUp.from_dict(follow_ups_item_data)

            follow_ups.append(follow_ups_item)

        follow_up_count = d.pop("follow_up_count")

        overdue_follow_up_count = d.pop("overdue_follow_up_count")

        follow_ups_truncated = d.pop("follow_ups_truncated")

        actions = []
        _actions = d.pop("actions")
        for actions_item_data in _actions:
            actions_item = OperationWorkflowCaseAction.from_dict(actions_item_data)

            actions.append(actions_item)

        actions_truncated = d.pop("actions_truncated")

        timeline = OperationWorkflowCoordinationTimeline.from_dict(d.pop("timeline"))

        evidence_checked_at = datetime.datetime.fromisoformat(d.pop("evidence_checked_at"))

        coordination_checked_at = datetime.datetime.fromisoformat(d.pop("coordination_checked_at"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        limitations = cast(list[str], d.pop("limitations"))

        _obligation_context = d.pop("obligation_context", UNSET)
        obligation_context: OperationWorkflowObligationContext | Unset
        if isinstance(_obligation_context, Unset):
            obligation_context = UNSET
        else:
            obligation_context = OperationWorkflowObligationContext.from_dict(_obligation_context)

        _instance = d.pop("instance", UNSET)
        instance: OperationWorkflowInstanceSnapshot | Unset
        if isinstance(_instance, Unset):
            instance = UNSET
        else:
            instance = OperationWorkflowInstanceSnapshot.from_dict(_instance)

        operation_workflow_case = cls(
            obligation_assessments=obligation_assessments,
            obligation_history=obligation_history,
            obligation_history_count=obligation_history_count,
            obligation_history_truncated=obligation_history_truncated,
            obligation_history_limited=obligation_history_limited,
            obligation_history_checked_at=obligation_history_checked_at,
            next_actions=next_actions,
            next_action_count=next_action_count,
            next_actions_truncated=next_actions_truncated,
            playbook_progress=playbook_progress,
            guidance=guidance,
            guidance_status=guidance_status,
            guidance_truncated=guidance_truncated,
            status=status,
            summary=summary,
            follow_ups=follow_ups,
            follow_up_count=follow_up_count,
            overdue_follow_up_count=overdue_follow_up_count,
            follow_ups_truncated=follow_ups_truncated,
            actions=actions,
            actions_truncated=actions_truncated,
            timeline=timeline,
            evidence_checked_at=evidence_checked_at,
            coordination_checked_at=coordination_checked_at,
            evaluated_at=evaluated_at,
            limitations=limitations,
            obligation_context=obligation_context,
            instance=instance,
        )

        operation_workflow_case.additional_properties = d
        return operation_workflow_case

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
