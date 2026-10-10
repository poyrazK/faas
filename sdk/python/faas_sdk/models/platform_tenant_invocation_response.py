from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.platform_tenant_invocation_response_state import (
    PlatformTenantInvocationResponseState,
    check_platform_tenant_invocation_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantInvocationResponse")


@_attrs_define
class PlatformTenantInvocationResponse:
    """Status and result for customer work; omits account, deployment, original payload and headers."""

    id: UUID
    state: PlatformTenantInvocationResponseState
    method: str
    path: str
    attempts: int
    created_at: datetime.datetime
    result: Any | Unset = UNSET
    """JSON result returned by the guest, when available."""
    last_error: str | Unset = UNSET
    outcome: None | str | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        state: str = self.state

        method = self.method

        path = self.path

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        result = self.result

        last_error = self.last_error

        outcome: None | str | Unset
        if isinstance(self.outcome, Unset):
            outcome = UNSET
        else:
            outcome = self.outcome

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "state": state,
                "method": method,
                "path": path,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if result is not UNSET:
            field_dict["result"] = result
        if last_error is not UNSET:
            field_dict["last_error"] = last_error
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        state = check_platform_tenant_invocation_response_state(d.pop("state"))

        method = d.pop("method")

        path = d.pop("path")

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        result = d.pop("result", UNSET)

        last_error = d.pop("last_error", UNSET)

        def _parse_outcome(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        outcome = _parse_outcome(d.pop("outcome", UNSET))

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        platform_tenant_invocation_response = cls(
            id=id,
            state=state,
            method=method,
            path=path,
            attempts=attempts,
            created_at=created_at,
            result=result,
            last_error=last_error,
            outcome=outcome,
            completed_at=completed_at,
        )

        platform_tenant_invocation_response.additional_properties = d
        return platform_tenant_invocation_response

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
