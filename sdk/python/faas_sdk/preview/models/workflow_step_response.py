from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_step_response_skip_reason import (
    WorkflowStepResponseSkipReason,
    check_workflow_step_response_skip_reason,
)
from ..models.workflow_step_response_status import WorkflowStepResponseStatus, check_workflow_step_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowStepResponse")


@_attrs_define
class WorkflowStepResponse:
    """A step attempt within a durable workflow run."""

    step_name: str
    status: WorkflowStepResponseStatus
    attempt: int
    created_at: datetime.datetime
    retry_base: int | Unset = UNSET
    """Attempt number at the latest resume; attempts since this number consume the current retry budget."""
    for_each_parent: str | Unset = UNSET
    """Parent step name for a persisted iteration item."""
    for_each_index: int | Unset = UNSET
    """Stable zero-based index within the snapshotted list."""
    for_each_count: int | Unset = UNSET
    """Snapshotted item count on an initialized parent; zero is an empty batch."""
    input_: Any | Unset = UNSET
    output: Any | Unset = UNSET
    when_matched: bool | Unset = UNSET
    """Persisted guard decision; absent when the guard has not run or a dependency was skipped."""
    when_evaluated_at: datetime.datetime | Unset = UNSET
    skip_reason: WorkflowStepResponseSkipReason | Unset = UNSET
    """Why a pending step was skipped; contains no referenced customer values."""
    started_at: datetime.datetime | None | Unset = UNSET
    next_check_at: datetime.datetime | None | Unset = UNSET
    next_retry_at: datetime.datetime | None | Unset = UNSET
    finished_at: datetime.datetime | None | Unset = UNSET
    error: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        step_name = self.step_name

        status: str = self.status

        attempt = self.attempt

        created_at = self.created_at.isoformat()

        retry_base = self.retry_base

        for_each_parent = self.for_each_parent

        for_each_index = self.for_each_index

        for_each_count = self.for_each_count

        input_ = self.input_

        output = self.output

        when_matched = self.when_matched

        when_evaluated_at: str | Unset = UNSET
        if not isinstance(self.when_evaluated_at, Unset):
            when_evaluated_at = self.when_evaluated_at.isoformat()

        skip_reason: str | Unset = UNSET
        if not isinstance(self.skip_reason, Unset):
            skip_reason = self.skip_reason

        started_at: None | str | Unset
        if isinstance(self.started_at, Unset):
            started_at = UNSET
        elif isinstance(self.started_at, datetime.datetime):
            started_at = self.started_at.isoformat()
        else:
            started_at = self.started_at

        next_check_at: None | str | Unset
        if isinstance(self.next_check_at, Unset):
            next_check_at = UNSET
        elif isinstance(self.next_check_at, datetime.datetime):
            next_check_at = self.next_check_at.isoformat()
        else:
            next_check_at = self.next_check_at

        next_retry_at: None | str | Unset
        if isinstance(self.next_retry_at, Unset):
            next_retry_at = UNSET
        elif isinstance(self.next_retry_at, datetime.datetime):
            next_retry_at = self.next_retry_at.isoformat()
        else:
            next_retry_at = self.next_retry_at

        finished_at: None | str | Unset
        if isinstance(self.finished_at, Unset):
            finished_at = UNSET
        elif isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        error: None | str | Unset
        if isinstance(self.error, Unset):
            error = UNSET
        else:
            error = self.error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "step_name": step_name,
                "status": status,
                "attempt": attempt,
                "created_at": created_at,
            }
        )
        if retry_base is not UNSET:
            field_dict["retry_base"] = retry_base
        if for_each_parent is not UNSET:
            field_dict["for_each_parent"] = for_each_parent
        if for_each_index is not UNSET:
            field_dict["for_each_index"] = for_each_index
        if for_each_count is not UNSET:
            field_dict["for_each_count"] = for_each_count
        if input_ is not UNSET:
            field_dict["input"] = input_
        if output is not UNSET:
            field_dict["output"] = output
        if when_matched is not UNSET:
            field_dict["when_matched"] = when_matched
        if when_evaluated_at is not UNSET:
            field_dict["when_evaluated_at"] = when_evaluated_at
        if skip_reason is not UNSET:
            field_dict["skip_reason"] = skip_reason
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if next_check_at is not UNSET:
            field_dict["next_check_at"] = next_check_at
        if next_retry_at is not UNSET:
            field_dict["next_retry_at"] = next_retry_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        step_name = d.pop("step_name")

        status = check_workflow_step_response_status(d.pop("status"))

        attempt = d.pop("attempt")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        retry_base = d.pop("retry_base", UNSET)

        for_each_parent = d.pop("for_each_parent", UNSET)

        for_each_index = d.pop("for_each_index", UNSET)

        for_each_count = d.pop("for_each_count", UNSET)

        input_ = d.pop("input", UNSET)

        output = d.pop("output", UNSET)

        when_matched = d.pop("when_matched", UNSET)

        _when_evaluated_at = d.pop("when_evaluated_at", UNSET)
        when_evaluated_at: datetime.datetime | Unset
        if isinstance(_when_evaluated_at, Unset):
            when_evaluated_at = UNSET
        else:
            when_evaluated_at = datetime.datetime.fromisoformat(_when_evaluated_at)

        _skip_reason = d.pop("skip_reason", UNSET)
        skip_reason: WorkflowStepResponseSkipReason | Unset
        if isinstance(_skip_reason, Unset):
            skip_reason = UNSET
        else:
            skip_reason = check_workflow_step_response_skip_reason(_skip_reason)

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

        def _parse_next_check_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_check_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_check_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        next_check_at = _parse_next_check_at(d.pop("next_check_at", UNSET))

        def _parse_next_retry_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_retry_at_type_0 = datetime.datetime.fromisoformat(data)

                return next_retry_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        next_retry_at = _parse_next_retry_at(d.pop("next_retry_at", UNSET))

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

        def _parse_error(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        error = _parse_error(d.pop("error", UNSET))

        workflow_step_response = cls(
            step_name=step_name,
            status=status,
            attempt=attempt,
            created_at=created_at,
            retry_base=retry_base,
            for_each_parent=for_each_parent,
            for_each_index=for_each_index,
            for_each_count=for_each_count,
            input_=input_,
            output=output,
            when_matched=when_matched,
            when_evaluated_at=when_evaluated_at,
            skip_reason=skip_reason,
            started_at=started_at,
            next_check_at=next_check_at,
            next_retry_at=next_retry_at,
            finished_at=finished_at,
            error=error,
        )

        workflow_step_response.additional_properties = d
        return workflow_step_response

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
