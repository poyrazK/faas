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
    from ..models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
    from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
    from ..models.operation_workflow_playbook_guidance import OperationWorkflowPlaybookGuidance
    from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp


T = TypeVar("T", bound="OperationWorkflowCase")


@_attrs_define
class OperationWorkflowCase:
    """OperationWorkflowCase. Current evidence and advisory coordination for an exact scoped business workflow."""

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
    instance: OperationWorkflowInstanceSnapshot | Unset = UNSET
    """Grouped view of the selected contract's declared steps and allowed transitions, current explicit state, and
    transition-history page for one workflow instance. Allowed transitions are contract edges by target Operation;
    the application still checks its business row and authorization before using one. Page-scoped facts follow the
    milestone cursor; retention-wide step summaries cover all matching facts still retained under the normal
    Operation retention rules."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
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

        instance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.instance, Unset):
            instance = self.instance.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
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
        if instance is not UNSET:
            field_dict["instance"] = instance

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_case_action import OperationWorkflowCaseAction
        from ..models.operation_workflow_coordination_timeline import OperationWorkflowCoordinationTimeline
        from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
        from ..models.operation_workflow_playbook_guidance import OperationWorkflowPlaybookGuidance
        from ..models.operation_workflow_readiness_follow_up import OperationWorkflowReadinessFollowUp

        d = dict(src_dict)
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

        _instance = d.pop("instance", UNSET)
        instance: OperationWorkflowInstanceSnapshot | Unset
        if isinstance(_instance, Unset):
            instance = UNSET
        else:
            instance = OperationWorkflowInstanceSnapshot.from_dict(_instance)

        operation_workflow_case = cls(
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
