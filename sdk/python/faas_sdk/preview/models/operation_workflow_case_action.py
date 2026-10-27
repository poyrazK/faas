from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowCaseAction")


@_attrs_define
class OperationWorkflowCaseAction:
    """OperationWorkflowCaseAction. Current evidence and advisory coordination for an exact scoped business workflow."""

    kind: str
    """OperationWorkflowCaseAction.kind. Advisory suggestion category; never an execution command."""
    summary: str
    """OperationWorkflowCaseAction.summary. Evidence-based human next step."""
    owner: str | Unset = UNSET
    """OperationWorkflowCaseAction.owner. Application blocker owner, verification owner or separate human
    coordination owner depending on kind."""
    operation_id: UUID | Unset = UNSET
    """OperationWorkflowCaseAction.operation_id. Retained reporting operation evidence link."""
    subscription_id: UUID | Unset = UNSET
    """OperationWorkflowCaseAction.subscription_id. Source readiness inbox for a follow-up suggestion."""
    sequence: int | Unset = UNSET
    """OperationWorkflowCaseAction.sequence. Retained source notification sequence."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind

        summary = self.summary

        owner = self.owner

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        subscription_id: str | Unset = UNSET
        if not isinstance(self.subscription_id, Unset):
            subscription_id = str(self.subscription_id)

        sequence = self.sequence

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "summary": summary,
            }
        )
        if owner is not UNSET:
            field_dict["owner"] = owner
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if subscription_id is not UNSET:
            field_dict["subscription_id"] = subscription_id
        if sequence is not UNSET:
            field_dict["sequence"] = sequence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = d.pop("kind")

        summary = d.pop("summary")

        owner = d.pop("owner", UNSET)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        _subscription_id = d.pop("subscription_id", UNSET)
        subscription_id: UUID | Unset
        if isinstance(_subscription_id, Unset):
            subscription_id = UNSET
        else:
            subscription_id = UUID(_subscription_id)

        sequence = d.pop("sequence", UNSET)

        operation_workflow_case_action = cls(
            kind=kind,
            summary=summary,
            owner=owner,
            operation_id=operation_id,
            subscription_id=subscription_id,
            sequence=sequence,
        )

        operation_workflow_case_action.additional_properties = d
        return operation_workflow_case_action

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
