from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.delayed_task_response_state import DelayedTaskResponseState, check_delayed_task_response_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DelayedTaskResponse")


@_attrs_define
class DelayedTaskResponse:
    """Delayed task create/get/list shape with lifecycle and result metadata."""

    id: str
    scheduled_at: datetime.datetime
    state: DelayedTaskResponseState
    app_id: str | Unset = UNSET
    method: str | Unset = UNSET
    path: str | Unset = UNSET
    attempts: int | Unset = UNSET
    last_error: str | Unset = UNSET
    result: Any | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        scheduled_at = self.scheduled_at.isoformat()

        state: str = self.state

        app_id = self.app_id

        method = self.method

        path = self.path

        attempts = self.attempts

        last_error = self.last_error

        result = self.result

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scheduled_at": scheduled_at,
                "state": state,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path
        if attempts is not UNSET:
            field_dict["attempts"] = attempts
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if result is not UNSET:
            field_dict["result"] = result
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        scheduled_at = datetime.datetime.fromisoformat(d.pop("scheduled_at"))

        state = check_delayed_task_response_state(d.pop("state"))

        app_id = d.pop("app_id", UNSET)

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        attempts = d.pop("attempts", UNSET)

        last_error = d.pop("last_error", UNSET)

        result = d.pop("result", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        delayed_task_response = cls(
            id=id,
            scheduled_at=scheduled_at,
            state=state,
            app_id=app_id,
            method=method,
            path=path,
            attempts=attempts,
            last_error=last_error,
            result=result,
            created_at=created_at,
            completed_at=completed_at,
        )

        delayed_task_response.additional_properties = d
        return delayed_task_response

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
