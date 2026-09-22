from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountTraceInvocation")


@_attrs_define
class AccountTraceInvocation:
    """Safe durable invocation lifecycle projection linked to the trace."""

    app: str
    id: str
    source: str
    state: str
    attempts: int
    created_at: datetime.datetime
    queue_name: str | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    traceparent: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        id = self.id

        source = self.source

        state = self.state

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        queue_name = self.queue_name

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        traceparent = self.traceparent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "id": id,
                "source": source,
                "state": state,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if queue_name is not UNSET:
            field_dict["queue_name"] = queue_name
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if traceparent is not UNSET:
            field_dict["traceparent"] = traceparent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app = d.pop("app")

        id = d.pop("id")

        source = d.pop("source")

        state = d.pop("state")

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        queue_name = d.pop("queue_name", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        traceparent = d.pop("traceparent", UNSET)

        account_trace_invocation = cls(
            app=app,
            id=id,
            source=source,
            state=state,
            attempts=attempts,
            created_at=created_at,
            queue_name=queue_name,
            completed_at=completed_at,
            traceparent=traceparent,
        )

        account_trace_invocation.additional_properties = d
        return account_trace_invocation

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
