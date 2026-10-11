from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_step_blocker_escalations import OperationWorkflowStepBlockerEscalations
    from ..models.operation_workflow_step_state_sla_budget_seconds import OperationWorkflowStepStateSlaBudgetSeconds
    from ..models.operation_workflow_step_state_sla_warning_percent import OperationWorkflowStepStateSlaWarningPercent
    from ..models.operation_workflow_step_state_stale_after_seconds import OperationWorkflowStepStateStaleAfterSeconds
    from ..models.operation_workflow_transition import OperationWorkflowTransition


T = TypeVar("T", bound="OperationWorkflowStep")


@_attrs_define
class OperationWorkflowStep:
    """App-declared read-only workflow step represented by an observed milestone. New declarations select the instance ID
    from the validated public milestone payload using the pinned JSON Pointer.

    """

    workflow: str
    title: str
    step: str
    label: str
    milestone: str
    position: int
    blocker_escalations: OperationWorkflowStepBlockerEscalations | Unset = UNSET
    """Versioned policies by blocker code. All steps in one workflow definition must agree. Policies only recommend
    escalation for active workflows with known blocker age."""
    allow_reconciliation: bool | Unset = UNSET
    """Explicit permission for evidence-backed state reconciliation snapshots."""
    version: int | Unset = UNSET
    """Explicit workflow contract version. Legacy definitions that omit it have effective version 1."""
    states: list[str] | Unset = UNSET
    """App-declared business state vocabulary pinned with this workflow mapping."""
    terminal_states: list[str] | Unset = UNSET
    """App-declared terminal states, which must be in states and cannot have outgoing transitions."""
    state_sla_warning_percent: OperationWorkflowStepStateSlaWarningPercent | Unset = UNSET
    """Optional whole-percent warning thresholds for states with SLA budgets. At the threshold a known current
    visit becomes at_risk until its budget is breached. Omitted states have no early warning. All steps must agree;
    publish a new workflow contract version when changing thresholds."""
    state_sla_budget_seconds: OperationWorkflowStepStateSlaBudgetSeconds | Unset = UNSET
    """Optional observed state visit budgets. Keys must be declared nonterminal states. All workflow steps in one
    definition must agree; publish a new workflow contract version for budget changes. Metadata updates do not reset
    a visit clock. Unknown retained entry times do not imply a breach."""
    state_stale_after_seconds: OperationWorkflowStepStateStaleAfterSeconds | Unset = UNSET
    """App-declared age thresholds in seconds for active states. Keys must be states and cannot be terminal states."""
    transitions: list[OperationWorkflowTransition] | Unset = UNSET
    """App-declared allowed state edges pinned with this workflow mapping."""
    transitions_declared: bool | Unset = UNSET
    """True when the workflow declares transitions, including when this Operation has no scoped edges."""
    instance_id_from: str | Unset = UNSET
    """JSON Pointer to a stable workflow-run ID in this milestone's payload; omitted by older pinned definitions."""
    instance_id: str | Unset = UNSET
    """App-provided workflow-run ID selected from this observed milestone."""

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        title = self.title

        step = self.step

        label = self.label

        milestone = self.milestone

        position = self.position

        blocker_escalations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.blocker_escalations, Unset):
            blocker_escalations = self.blocker_escalations.to_dict()

        allow_reconciliation = self.allow_reconciliation

        version = self.version

        states: list[str] | Unset = UNSET
        if not isinstance(self.states, Unset):
            states = self.states

        terminal_states: list[str] | Unset = UNSET
        if not isinstance(self.terminal_states, Unset):
            terminal_states = self.terminal_states

        state_sla_warning_percent: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state_sla_warning_percent, Unset):
            state_sla_warning_percent = self.state_sla_warning_percent.to_dict()

        state_sla_budget_seconds: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state_sla_budget_seconds, Unset):
            state_sla_budget_seconds = self.state_sla_budget_seconds.to_dict()

        state_stale_after_seconds: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state_stale_after_seconds, Unset):
            state_stale_after_seconds = self.state_stale_after_seconds.to_dict()

        transitions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.transitions, Unset):
            transitions = []
            for transitions_item_data in self.transitions:
                transitions_item = transitions_item_data.to_dict()
                transitions.append(transitions_item)

        transitions_declared = self.transitions_declared

        instance_id_from = self.instance_id_from

        instance_id = self.instance_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "title": title,
                "step": step,
                "label": label,
                "milestone": milestone,
                "position": position,
            }
        )
        if blocker_escalations is not UNSET:
            field_dict["blocker_escalations"] = blocker_escalations
        if allow_reconciliation is not UNSET:
            field_dict["allow_reconciliation"] = allow_reconciliation
        if version is not UNSET:
            field_dict["version"] = version
        if states is not UNSET:
            field_dict["states"] = states
        if terminal_states is not UNSET:
            field_dict["terminal_states"] = terminal_states
        if state_sla_warning_percent is not UNSET:
            field_dict["state_sla_warning_percent"] = state_sla_warning_percent
        if state_sla_budget_seconds is not UNSET:
            field_dict["state_sla_budget_seconds"] = state_sla_budget_seconds
        if state_stale_after_seconds is not UNSET:
            field_dict["state_stale_after_seconds"] = state_stale_after_seconds
        if transitions is not UNSET:
            field_dict["transitions"] = transitions
        if transitions_declared is not UNSET:
            field_dict["transitions_declared"] = transitions_declared
        if instance_id_from is not UNSET:
            field_dict["instance_id_from"] = instance_id_from
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_step_blocker_escalations import OperationWorkflowStepBlockerEscalations
        from ..models.operation_workflow_step_state_sla_budget_seconds import OperationWorkflowStepStateSlaBudgetSeconds
        from ..models.operation_workflow_step_state_sla_warning_percent import (
            OperationWorkflowStepStateSlaWarningPercent,
        )
        from ..models.operation_workflow_step_state_stale_after_seconds import (
            OperationWorkflowStepStateStaleAfterSeconds,
        )
        from ..models.operation_workflow_transition import OperationWorkflowTransition

        d = dict(src_dict)
        workflow = d.pop("workflow")

        title = d.pop("title")

        step = d.pop("step")

        label = d.pop("label")

        milestone = d.pop("milestone")

        position = d.pop("position")

        _blocker_escalations = d.pop("blocker_escalations", UNSET)
        blocker_escalations: OperationWorkflowStepBlockerEscalations | Unset
        if isinstance(_blocker_escalations, Unset):
            blocker_escalations = UNSET
        else:
            blocker_escalations = OperationWorkflowStepBlockerEscalations.from_dict(_blocker_escalations)

        allow_reconciliation = d.pop("allow_reconciliation", UNSET)

        version = d.pop("version", UNSET)

        states = cast(list[str], d.pop("states", UNSET))

        terminal_states = cast(list[str], d.pop("terminal_states", UNSET))

        _state_sla_warning_percent = d.pop("state_sla_warning_percent", UNSET)
        state_sla_warning_percent: OperationWorkflowStepStateSlaWarningPercent | Unset
        if isinstance(_state_sla_warning_percent, Unset):
            state_sla_warning_percent = UNSET
        else:
            state_sla_warning_percent = OperationWorkflowStepStateSlaWarningPercent.from_dict(
                _state_sla_warning_percent
            )

        _state_sla_budget_seconds = d.pop("state_sla_budget_seconds", UNSET)
        state_sla_budget_seconds: OperationWorkflowStepStateSlaBudgetSeconds | Unset
        if isinstance(_state_sla_budget_seconds, Unset):
            state_sla_budget_seconds = UNSET
        else:
            state_sla_budget_seconds = OperationWorkflowStepStateSlaBudgetSeconds.from_dict(_state_sla_budget_seconds)

        _state_stale_after_seconds = d.pop("state_stale_after_seconds", UNSET)
        state_stale_after_seconds: OperationWorkflowStepStateStaleAfterSeconds | Unset
        if isinstance(_state_stale_after_seconds, Unset):
            state_stale_after_seconds = UNSET
        else:
            state_stale_after_seconds = OperationWorkflowStepStateStaleAfterSeconds.from_dict(
                _state_stale_after_seconds
            )

        _transitions = d.pop("transitions", UNSET)
        transitions: list[OperationWorkflowTransition] | Unset = UNSET
        if _transitions is not UNSET:
            transitions = []
            for transitions_item_data in _transitions:
                transitions_item = OperationWorkflowTransition.from_dict(transitions_item_data)

                transitions.append(transitions_item)

        transitions_declared = d.pop("transitions_declared", UNSET)

        instance_id_from = d.pop("instance_id_from", UNSET)

        instance_id = d.pop("instance_id", UNSET)

        operation_workflow_step = cls(
            workflow=workflow,
            title=title,
            step=step,
            label=label,
            milestone=milestone,
            position=position,
            blocker_escalations=blocker_escalations,
            allow_reconciliation=allow_reconciliation,
            version=version,
            states=states,
            terminal_states=terminal_states,
            state_sla_warning_percent=state_sla_warning_percent,
            state_sla_budget_seconds=state_sla_budget_seconds,
            state_stale_after_seconds=state_stale_after_seconds,
            transitions=transitions,
            transitions_declared=transitions_declared,
            instance_id_from=instance_id_from,
            instance_id=instance_id,
        )

        return operation_workflow_step
