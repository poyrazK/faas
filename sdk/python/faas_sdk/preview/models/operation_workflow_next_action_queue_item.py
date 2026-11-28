from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
    from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase


T = TypeVar("T", bound="OperationWorkflowNextActionQueueItem")


@_attrs_define
class OperationWorkflowNextActionQueueItem:
    """Current source action shown once across its matching workflow cases. Owner and due filters apply to the action, not
    parent coordination. Shared record controls affect all covered cases; evidence and case lists respect the
    customer/workflow selector.

    """

    action: OperationWorkflowCaseNextAction
    """One current coordination obligation or suggestion for an exact workflow case, including the responsible
    label, its own deadline and a source inbox link. Shared plans may span cases; evidence includes only references
    matching this case. This does not authorize application execution or infer business completion."""
    cases: list[OperationWorkflowNextActionCase]
    scope: str
    subscription_tenant_id: UUID | Unset = UNSET
    """Source subscription customer selector for opening its controls, distinct from affected case customers."""

    def to_dict(self) -> dict[str, Any]:
        action = self.action.to_dict()

        cases = []
        for cases_item_data in self.cases:
            cases_item = cases_item_data.to_dict()
            cases.append(cases_item)

        scope = self.scope

        subscription_tenant_id: str | Unset = UNSET
        if not isinstance(self.subscription_tenant_id, Unset):
            subscription_tenant_id = str(self.subscription_tenant_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "action": action,
                "cases": cases,
                "scope": scope,
            }
        )
        if subscription_tenant_id is not UNSET:
            field_dict["subscription_tenant_id"] = subscription_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_case_next_action import OperationWorkflowCaseNextAction
        from ..models.operation_workflow_next_action_case import OperationWorkflowNextActionCase

        d = dict(src_dict)
        action = OperationWorkflowCaseNextAction.from_dict(d.pop("action"))

        cases = []
        _cases = d.pop("cases")
        for cases_item_data in _cases:
            cases_item = OperationWorkflowNextActionCase.from_dict(cases_item_data)

            cases.append(cases_item)

        scope = d.pop("scope")

        _subscription_tenant_id = d.pop("subscription_tenant_id", UNSET)
        subscription_tenant_id: UUID | Unset
        if isinstance(_subscription_tenant_id, Unset):
            subscription_tenant_id = UNSET
        else:
            subscription_tenant_id = UUID(_subscription_tenant_id)

        operation_workflow_next_action_queue_item = cls(
            action=action,
            cases=cases,
            scope=scope,
            subscription_tenant_id=subscription_tenant_id,
        )

        return operation_workflow_next_action_queue_item
