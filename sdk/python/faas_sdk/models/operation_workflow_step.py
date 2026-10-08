from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
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
    allow_reconciliation: bool | Unset = UNSET
    version: int | Unset = UNSET
    """Explicit workflow contract version. Legacy definitions that omit it have effective version 1."""
    states: list[str] | Unset = UNSET
    """App-declared business state vocabulary pinned with this workflow mapping."""
    terminal_states: list[str] | Unset = UNSET
    """App-declared terminal states, which must be in states and cannot have outgoing transitions."""
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

        version = self.version

        states: list[str] | Unset = UNSET
        if not isinstance(self.states, Unset):
            states = self.states

        terminal_states: list[str] | Unset = UNSET
        if not isinstance(self.terminal_states, Unset):
            terminal_states = self.terminal_states

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
        if version is not UNSET:
            field_dict["version"] = version
        if states is not UNSET:
            field_dict["states"] = states
        if terminal_states is not UNSET:
            field_dict["terminal_states"] = terminal_states
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

        if self.allow_reconciliation is not UNSET: field_dict["allow_reconciliation"]=self.allow_reconciliation
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
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

        version = d.pop("version", UNSET)

        states = cast(list[str], d.pop("states", UNSET))

        terminal_states = cast(list[str], d.pop("terminal_states", UNSET))

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
            allow_reconciliation=d.pop("allow_reconciliation",UNSET),
            title=title,
            step=step,
            label=label,
            milestone=milestone,
            position=position,
            version=version,
            states=states,
            terminal_states=terminal_states,
            state_stale_after_seconds=state_stale_after_seconds,
            transitions=transitions,
            transitions_declared=transitions_declared,
            instance_id_from=instance_id_from,
            instance_id=instance_id,
        )

        return operation_workflow_step
