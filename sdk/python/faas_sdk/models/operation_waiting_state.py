from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_waiting_state_approver import (
    OperationWaitingStateApprover,
    check_operation_waiting_state_approver,
)
from ..models.operation_waiting_state_kind import OperationWaitingStateKind, check_operation_waiting_state_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWaitingState")


@_attrs_define
class OperationWaitingState:
    """Why a waiting operation holds no execution. until is the timer's wake time or the approval deadline."""

    kind: OperationWaitingStateKind
    step: str
    since: datetime.datetime
    until: datetime.datetime | Unset = UNSET
    approver: OperationWaitingStateApprover | Unset = UNSET
    event: str | Unset = UNSET
    """The event an event wait expects (ADR-1197)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        step = self.step

        since = self.since.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        approver: str | Unset = UNSET
        if not isinstance(self.approver, Unset):
            approver = self.approver

        event = self.event

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "step": step,
                "since": since,
            }
        )
        if until is not UNSET:
            field_dict["until"] = until
        if approver is not UNSET:
            field_dict["approver"] = approver
        if event is not UNSET:
            field_dict["event"] = event

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_operation_waiting_state_kind(d.pop("kind"))

        step = d.pop("step")

        since = datetime.datetime.fromisoformat(d.pop("since"))

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        _approver = d.pop("approver", UNSET)
        approver: OperationWaitingStateApprover | Unset
        if isinstance(_approver, Unset):
            approver = UNSET
        else:
            approver = check_operation_waiting_state_approver(_approver)

        event = d.pop("event", UNSET)

        operation_waiting_state = cls(
            kind=kind,
            step=step,
            since=since,
            until=until,
            approver=approver,
            event=event,
        )

        operation_waiting_state.additional_properties = d
        return operation_waiting_state

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
