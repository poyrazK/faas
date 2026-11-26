from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_sandbox_run_step_invocation_state import (
    APISandboxRunStepInvocationState,
    check_api_sandbox_run_step_invocation_state,
)
from ..models.api_sandbox_run_step_kind import APISandboxRunStepKind, check_api_sandbox_run_step_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISandboxRunStep")


@_attrs_define
class APISandboxRunStep:
    """Redacted evidence for one request or webhook step in a scenario."""

    index: int
    title: str
    method: str
    duration_ms: int
    path: str | Unset = UNSET
    """Saved request path without its query string."""
    partner_request: bool | Unset = UNSET
    acknowledgement_dropped: bool | Unset = UNSET
    """Gregale intentionally ignored the first successful HTTP response."""
    partner_applied_count: int | Unset = UNSET
    """Business effects reported by the partner."""
    kind: APISandboxRunStepKind | Unset = UNSET
    event_type: str | Unset = UNSET
    invocation_id: UUID | Unset = UNSET
    invocation_state: APISandboxRunStepInvocationState | Unset = UNSET
    delivery_attempts: int | Unset = UNSET
    delivery_id: UUID | Unset = UNSET
    attempt_number: int | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    expected_status: int | Unset = UNSET
    http_status: int | Unset = UNSET
    request_id: str | Unset = UNSET
    failure_code: str | Unset = UNSET
    failed_assertion: int | Unset = UNSET
    failed_pointer: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        index = self.index

        title = self.title

        method = self.method

        duration_ms = self.duration_ms

        path = self.path

        partner_request = self.partner_request

        acknowledgement_dropped = self.acknowledgement_dropped

        partner_applied_count = self.partner_applied_count

        kind: str | Unset = UNSET
        if not isinstance(self.kind, Unset):
            kind = self.kind

        event_type = self.event_type

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        invocation_state: str | Unset = UNSET
        if not isinstance(self.invocation_state, Unset):
            invocation_state = self.invocation_state

        delivery_attempts = self.delivery_attempts

        delivery_id: str | Unset = UNSET
        if not isinstance(self.delivery_id, Unset):
            delivery_id = str(self.delivery_id)

        attempt_number = self.attempt_number

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        expected_status = self.expected_status

        http_status = self.http_status

        request_id = self.request_id

        failure_code = self.failure_code

        failed_assertion = self.failed_assertion

        failed_pointer = self.failed_pointer

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "index": index,
                "title": title,
                "method": method,
                "duration_ms": duration_ms,
            }
        )
        if path is not UNSET:
            field_dict["path"] = path
        if partner_request is not UNSET:
            field_dict["partner_request"] = partner_request
        if acknowledgement_dropped is not UNSET:
            field_dict["acknowledgement_dropped"] = acknowledgement_dropped
        if partner_applied_count is not UNSET:
            field_dict["partner_applied_count"] = partner_applied_count
        if kind is not UNSET:
            field_dict["kind"] = kind
        if event_type is not UNSET:
            field_dict["event_type"] = event_type
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if invocation_state is not UNSET:
            field_dict["invocation_state"] = invocation_state
        if delivery_attempts is not UNSET:
            field_dict["delivery_attempts"] = delivery_attempts
        if delivery_id is not UNSET:
            field_dict["delivery_id"] = delivery_id
        if attempt_number is not UNSET:
            field_dict["attempt_number"] = attempt_number
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if expected_status is not UNSET:
            field_dict["expected_status"] = expected_status
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if failed_assertion is not UNSET:
            field_dict["failed_assertion"] = failed_assertion
        if failed_pointer is not UNSET:
            field_dict["failed_pointer"] = failed_pointer

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        index = d.pop("index")

        title = d.pop("title")

        method = d.pop("method")

        duration_ms = d.pop("duration_ms")

        path = d.pop("path", UNSET)

        partner_request = d.pop("partner_request", UNSET)

        acknowledgement_dropped = d.pop("acknowledgement_dropped", UNSET)

        partner_applied_count = d.pop("partner_applied_count", UNSET)

        _kind = d.pop("kind", UNSET)
        kind: APISandboxRunStepKind | Unset
        if isinstance(_kind, Unset):
            kind = UNSET
        else:
            kind = check_api_sandbox_run_step_kind(_kind)

        event_type = d.pop("event_type", UNSET)

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _invocation_state = d.pop("invocation_state", UNSET)
        invocation_state: APISandboxRunStepInvocationState | Unset
        if isinstance(_invocation_state, Unset):
            invocation_state = UNSET
        else:
            invocation_state = check_api_sandbox_run_step_invocation_state(_invocation_state)

        delivery_attempts = d.pop("delivery_attempts", UNSET)

        _delivery_id = d.pop("delivery_id", UNSET)
        delivery_id: UUID | Unset
        if isinstance(_delivery_id, Unset):
            delivery_id = UNSET
        else:
            delivery_id = UUID(_delivery_id)

        attempt_number = d.pop("attempt_number", UNSET)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        expected_status = d.pop("expected_status", UNSET)

        http_status = d.pop("http_status", UNSET)

        request_id = d.pop("request_id", UNSET)

        failure_code = d.pop("failure_code", UNSET)

        failed_assertion = d.pop("failed_assertion", UNSET)

        failed_pointer = d.pop("failed_pointer", UNSET)

        api_sandbox_run_step = cls(
            index=index,
            title=title,
            method=method,
            duration_ms=duration_ms,
            path=path,
            partner_request=partner_request,
            acknowledgement_dropped=acknowledgement_dropped,
            partner_applied_count=partner_applied_count,
            kind=kind,
            event_type=event_type,
            invocation_id=invocation_id,
            invocation_state=invocation_state,
            delivery_attempts=delivery_attempts,
            delivery_id=delivery_id,
            attempt_number=attempt_number,
            observed_at=observed_at,
            expected_status=expected_status,
            http_status=http_status,
            request_id=request_id,
            failure_code=failure_code,
            failed_assertion=failed_assertion,
            failed_pointer=failed_pointer,
        )

        api_sandbox_run_step.additional_properties = d
        return api_sandbox_run_step

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
