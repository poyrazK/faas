from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_run_response_status import WorkflowRunResponseStatus, check_workflow_run_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRunResponse")


@_attrs_define
class WorkflowRunResponse:
    """A persisted durable workflow run (ADR-081)."""

    id: UUID
    app_id: UUID
    workflow_name: str
    status: WorkflowRunResponseStatus
    scheduled_for: datetime.datetime
    created_at: datetime.datetime
    updated_at: datetime.datetime
    current_step: None | str | Unset = UNSET
    input_: Any | Unset = UNSET
    output: Any | Unset = UNSET
    started_at: datetime.datetime | None | Unset = UNSET
    finished_at: datetime.datetime | None | Unset = UNSET
    last_error: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        workflow_name = self.workflow_name

        status: str = self.status

        scheduled_for = self.scheduled_for.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        current_step: None | str | Unset
        if isinstance(self.current_step, Unset):
            current_step = UNSET
        else:
            current_step = self.current_step

        input_ = self.input_

        output = self.output

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        last_error: None | str | Unset
        if isinstance(self.last_error, Unset):
            last_error = UNSET
        else:
            last_error = self.last_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "workflow_name": workflow_name,
                "status": status,
                "scheduled_for": scheduled_for,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if current_step is not UNSET:
            field_dict["current_step"] = current_step
        if input_ is not UNSET:
            field_dict["input"] = input_
        if output is not UNSET:
            field_dict["output"] = output
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if last_error is not UNSET:
            field_dict["last_error"] = last_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        workflow_name = d.pop("workflow_name")

        status = check_workflow_run_response_status(d.pop("status"))

        scheduled_for = datetime.datetime.fromisoformat(d.pop("scheduled_for"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        def _parse_current_step(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        current_step = _parse_current_step(d.pop("current_step", UNSET))

        input_ = d.pop("input", UNSET)

        output = d.pop("output", UNSET)

        def _parse_started_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                started_at_type_0 = datetime.datetime.fromisoformat(data)

                return started_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        started_at = _parse_started_at(d.pop("started_at", UNSET))

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

        def _parse_last_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        last_error = _parse_last_error(d.pop("last_error", UNSET))

        workflow_run_response = cls(
            id=id,
            app_id=app_id,
            workflow_name=workflow_name,
            status=status,
            scheduled_for=scheduled_for,
            created_at=created_at,
            updated_at=updated_at,
            current_step=current_step,
            input_=input_,
            output=output,
            started_at=started_at,
            finished_at=finished_at,
            last_error=last_error,
        )

        workflow_run_response.additional_properties = d
        return workflow_run_response

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
