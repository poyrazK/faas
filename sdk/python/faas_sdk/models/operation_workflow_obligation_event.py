from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_obligation_event_kind import (
    OperationWorkflowObligationEventKind,
    check_operation_workflow_obligation_event_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
    from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
    from ..models.operation_workflow_obligation_replacement import OperationWorkflowObligationReplacement
    from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding


T = TypeVar("T", bound="OperationWorkflowObligationEvent")


@_attrs_define
class OperationWorkflowObligationEvent:
    """Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment
    snapshot, not a newly audited write. Available event coverage is partial.

    """

    kind: OperationWorkflowObligationEventKind
    at: datetime.datetime
    explanation: str
    fulfillment: OperationWorkflowObligationFulfillment | Unset = UNSET
    """Immutable human fulfillment receipt for one exact case and obligation cycle, linked to a retained
    application transition report. Does not close a shared source plan or assert business success or execution
    authority. Retained transition lookup scans at most 1000 exact-case reports."""
    target_binding: OperationWorkflowObligationTargetBinding | Unset = UNSET
    """One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason.
    Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed."""
    replacement: OperationWorkflowObligationReplacement | Unset = UNSET
    """Same-source obligation succession recorded atomically with a source change. Links require overlapping saved
    case coverage and removed/new cycles or explicit transition references; a retained terminal same-kind
    predecessor may link a later cycle. Generic new-cycle reasons describe succession rather than business
    causation. Reinstated known identities, unrelated cases and missing history are not linked. These edges do not
    prove fulfillment."""
    cases: list[OperationWorkflowNextActionCase] | Unset = UNSET
    """Affected case identities captured at the source change. Legacy receipts may omit them; case views exclude
    unscoped receipts."""
    action_kind: str | Unset = UNSET
    shared_plan: bool | Unset = UNSET
    obligation_id: str | Unset = UNSET
    """Exact obligation identity for mutation-time receipts; older inferred observations may omit it."""
    actor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        at = self.at.isoformat()

        explanation = self.explanation

        fulfillment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.fulfillment, Unset):
            fulfillment = self.fulfillment.to_dict()

        target_binding: dict[str, Any] | Unset = UNSET
        if not isinstance(self.target_binding, Unset):
            target_binding = self.target_binding.to_dict()

        replacement: dict[str, Any] | Unset = UNSET
        if not isinstance(self.replacement, Unset):
            replacement = self.replacement.to_dict()

        cases: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.cases, Unset):
            cases = []
            for cases_item_data in self.cases:
                cases_item = cases_item_data.to_dict()
                cases.append(cases_item)

        action_kind = self.action_kind

        shared_plan = self.shared_plan

        obligation_id = self.obligation_id

        actor = self.actor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "at": at,
                "explanation": explanation,
            }
        )
        if fulfillment is not UNSET:
            field_dict["fulfillment"] = fulfillment
        if target_binding is not UNSET:
            field_dict["target_binding"] = target_binding
        if replacement is not UNSET:
            field_dict["replacement"] = replacement
        if cases is not UNSET:
            field_dict["cases"] = cases
        if action_kind is not UNSET:
            field_dict["action_kind"] = action_kind
        if shared_plan is not UNSET:
            field_dict["shared_plan"] = shared_plan
        if obligation_id is not UNSET:
            field_dict["obligation_id"] = obligation_id
        if actor is not UNSET:
            field_dict["actor"] = actor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase
        from ..models.operation_workflow_obligation_fulfillment import OperationWorkflowObligationFulfillment
        from ..models.operation_workflow_obligation_replacement import OperationWorkflowObligationReplacement
        from ..models.operation_workflow_obligation_target_binding import OperationWorkflowObligationTargetBinding

        d = dict(src_dict)
        kind = check_operation_workflow_obligation_event_kind(d.pop("kind"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        explanation = d.pop("explanation")

        _fulfillment = d.pop("fulfillment", UNSET)
        fulfillment: OperationWorkflowObligationFulfillment | Unset
        if isinstance(_fulfillment, Unset):
            fulfillment = UNSET
        else:
            fulfillment = OperationWorkflowObligationFulfillment.from_dict(_fulfillment)

        _target_binding = d.pop("target_binding", UNSET)
        target_binding: OperationWorkflowObligationTargetBinding | Unset
        if isinstance(_target_binding, Unset):
            target_binding = UNSET
        else:
            target_binding = OperationWorkflowObligationTargetBinding.from_dict(_target_binding)

        _replacement = d.pop("replacement", UNSET)
        replacement: OperationWorkflowObligationReplacement | Unset
        if isinstance(_replacement, Unset):
            replacement = UNSET
        else:
            replacement = OperationWorkflowObligationReplacement.from_dict(_replacement)

        _cases = d.pop("cases", UNSET)
        cases: list[OperationWorkflowNextActionCase] | Unset = UNSET
        if _cases is not UNSET:
            cases = []
            for cases_item_data in _cases:
                cases_item = OperationWorkflowNextActionCase.from_dict(cases_item_data)

                cases.append(cases_item)

        action_kind = d.pop("action_kind", UNSET)

        shared_plan = d.pop("shared_plan", UNSET)

        obligation_id = d.pop("obligation_id", UNSET)

        actor = d.pop("actor", UNSET)

        operation_workflow_obligation_event = cls(
            kind=kind,
            at=at,
            explanation=explanation,
            fulfillment=fulfillment,
            target_binding=target_binding,
            replacement=replacement,
            cases=cases,
            action_kind=action_kind,
            shared_plan=shared_plan,
            obligation_id=obligation_id,
            actor=actor,
        )

        return operation_workflow_obligation_event
