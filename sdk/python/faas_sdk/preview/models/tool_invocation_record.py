from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_invocation_record_status import ToolInvocationRecordStatus, check_tool_invocation_record_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_invocation_delegation import ToolInvocationDelegation
    from ..models.tool_invocation_intent import ToolInvocationIntent
    from ..models.tool_invocation_outcome import ToolInvocationOutcome
    from ..models.tool_invocation_target import ToolInvocationTarget


T = TypeVar("T", bound="ToolInvocationRecord")


@_attrs_define
class ToolInvocationRecord:
    """Account-owned single-dispatch fact; cancellation after dispatch does not establish remote rollback or successful
    cancellation.

    """

    id: UUID
    account_id: UUID
    intent: ToolInvocationIntent
    """Explicit one-call consent with a canonical argument digest and optional opaque credential binding; no
    argument bodies or credentials."""
    target: ToolInvocationTarget
    """Immutable native target resolved from the exact owned publication and deployment source artifact."""
    status: ToolInvocationRecordStatus
    created_at: datetime.datetime
    deadline: datetime.datetime
    cancellation_requested: bool
    delegation: ToolInvocationDelegation | Unset = UNSET
    """Immutable authority resolved by APID; never accepted as caller-supplied invocation metadata."""
    dispatched_at: datetime.datetime | Unset = UNSET
    finished_at: datetime.datetime | Unset = UNSET
    outcome: ToolInvocationOutcome | Unset = UNSET
    """Worker-reported complete MCP result or explicit preflight/transport uncertainty. Unknown outcomes never
    authorize a replay."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        intent = self.intent.to_dict()

        target = self.target.to_dict()

        status: str = self.status

        created_at = self.created_at.isoformat()

        deadline = self.deadline.isoformat()

        cancellation_requested = self.cancellation_requested

        delegation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.delegation, Unset):
            delegation = self.delegation.to_dict()

        dispatched_at: str | Unset = UNSET
        if not isinstance(self.dispatched_at, Unset):
            dispatched_at = self.dispatched_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        outcome: dict[str, Any] | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "intent": intent,
                "target": target,
                "status": status,
                "created_at": created_at,
                "deadline": deadline,
                "cancellation_requested": cancellation_requested,
            }
        )
        if delegation is not UNSET:
            field_dict["delegation"] = delegation
        if dispatched_at is not UNSET:
            field_dict["dispatched_at"] = dispatched_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if outcome is not UNSET:
            field_dict["outcome"] = outcome

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_invocation_delegation import ToolInvocationDelegation
        from ..models.tool_invocation_intent import ToolInvocationIntent
        from ..models.tool_invocation_outcome import ToolInvocationOutcome
        from ..models.tool_invocation_target import ToolInvocationTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        intent = ToolInvocationIntent.from_dict(d.pop("intent"))

        target = ToolInvocationTarget.from_dict(d.pop("target"))

        status = check_tool_invocation_record_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        cancellation_requested = d.pop("cancellation_requested")

        _delegation = d.pop("delegation", UNSET)
        delegation: ToolInvocationDelegation | Unset
        if isinstance(_delegation, Unset):
            delegation = UNSET
        else:
            delegation = ToolInvocationDelegation.from_dict(_delegation)

        _dispatched_at = d.pop("dispatched_at", UNSET)
        dispatched_at: datetime.datetime | Unset
        if isinstance(_dispatched_at, Unset):
            dispatched_at = UNSET
        else:
            dispatched_at = datetime.datetime.fromisoformat(_dispatched_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        _outcome = d.pop("outcome", UNSET)
        outcome: ToolInvocationOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = ToolInvocationOutcome.from_dict(_outcome)

        tool_invocation_record = cls(
            id=id,
            account_id=account_id,
            intent=intent,
            target=target,
            status=status,
            created_at=created_at,
            deadline=deadline,
            cancellation_requested=cancellation_requested,
            delegation=delegation,
            dispatched_at=dispatched_at,
            finished_at=finished_at,
            outcome=outcome,
        )

        tool_invocation_record.additional_properties = d
        return tool_invocation_record

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
