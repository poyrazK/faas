from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_step_attempt_response_status import (
    WorkflowStepAttemptResponseStatus,
    check_workflow_step_attempt_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowStepAttemptResponse")


@_attrs_define
class WorkflowStepAttemptResponse:
    """One durable executor invocation for a workflow step."""

    attempt: int
    status: WorkflowStepAttemptResponseStatus
    started_at: datetime.datetime
    http_status: int | None | Unset = UNSET
    finished_at: datetime.datetime | None | Unset = UNSET
    next_attempt_at: datetime.datetime | None | Unset = UNSET
    error: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempt = self.attempt

        status: str = self.status

        started_at = self.started_at.isoformat()

        http_status: int | None | Unset
        if isinstance(self.http_status, Unset):
            http_status = UNSET
        else:
            http_status = self.http_status

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        next_attempt_at: None | str | Unset
        if isinstance(self.next_attempt_at, Unset):
            next_attempt_at = UNSET
        elif isinstance(self.next_attempt_at, datetime.datetime):
            next_attempt_at = self.next_attempt_at.isoformat()
        else:
            next_attempt_at = self.next_attempt_at

        error: None | str | Unset
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempt": attempt,
                "status": status,
                "started_at": started_at,
            }
        )
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        attempt = d.pop("attempt")

        status = check_workflow_step_attempt_response_status(d.pop("status"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        def _parse_http_status(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        http_status = _parse_http_status(d.pop("http_status", UNSET))

        def _parse_finished_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        finished_at = _parse_finished_at(d.pop("finished_at", UNSET))

        def _parse_next_attempt_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_attempt_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_attempt_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        next_attempt_at = _parse_next_attempt_at(d.pop("next_attempt_at", UNSET))

        def _parse_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error = _parse_error(d.pop("error", UNSET))

        workflow_step_attempt_response = cls(
            attempt=attempt,
            status=status,
            started_at=started_at,
            http_status=http_status,
            finished_at=finished_at,
            next_attempt_at=next_attempt_at,
            error=error,
        )

        workflow_step_attempt_response.additional_properties = d
        return workflow_step_attempt_response

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
