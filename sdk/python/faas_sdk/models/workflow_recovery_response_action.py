from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_response_action_kind import (
    WorkflowRecoveryResponseActionKind,
    check_workflow_recovery_response_action_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recovery_request import EventRecoveryRequest
    from ..models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest


T = TypeVar("T", bound="WorkflowRecoveryResponseAction")


@_attrs_define
class WorkflowRecoveryResponseAction:
    """One explicit cancellation or saved-failure retry target. Exactly one action request matches the kind and frozen
    identity.

    """

    kind: WorkflowRecoveryResponseActionKind
    position: int
    run_id: UUID
    cancellation: WorkflowRecoveryCancellationRequest | Unset = UNSET
    """Exact admitted recovery item and generation reviewed by an operator. Preview is advisory and commit rechecks
    eligibility. Operator identity is resolved from authentication."""
    retry: EventRecoveryRequest | Unset = UNSET
    """Select routing failures (default) or the latest replayable retained execution per application event
    consumer. With parent_job_id, saved parent failures remain selectable even when execution or receipt evidence
    has disappeared; admission skips changes rather than following newer work. Ordinary execution mode includes
    publication and materialized backfill recipients, excludes workflows and object notifications, and requires
    retained admission and execution records. Native workflow mode requires explicit workflow_runs and never selects
    event recipients. Creation freezes its own selection; preview is advisory."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        position = self.position

        run_id = str(self.run_id)

        cancellation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.cancellation, Unset):
            cancellation = self.cancellation.to_dict()

        retry: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retry, Unset):
            retry = self.retry.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "position": position,
                "run_id": run_id,
            }
        )
        if cancellation is not UNSET:
            field_dict["cancellation"] = cancellation
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recovery_request import EventRecoveryRequest
        from ..models.workflow_recovery_cancellation_request import WorkflowRecoveryCancellationRequest

        d = dict(src_dict)
        kind = check_workflow_recovery_response_action_kind(d.pop("kind"))

        position = d.pop("position")

        run_id = UUID(d.pop("run_id"))

        _cancellation = d.pop("cancellation", UNSET)
        cancellation: WorkflowRecoveryCancellationRequest | Unset
        if isinstance(_cancellation, Unset):
            cancellation = UNSET
        else:
            cancellation = WorkflowRecoveryCancellationRequest.from_dict(_cancellation)

        _retry = d.pop("retry", UNSET)
        retry: EventRecoveryRequest | Unset
        if isinstance(_retry, Unset):
            retry = UNSET
        else:
            retry = EventRecoveryRequest.from_dict(_retry)

        workflow_recovery_response_action = cls(
            kind=kind,
            position=position,
            run_id=run_id,
            cancellation=cancellation,
            retry=retry,
        )

        workflow_recovery_response_action.additional_properties = d
        return workflow_recovery_response_action

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
