from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.finalize_managed_realtime_auth_response_auth_mode import (
    FinalizeManagedRealtimeAuthResponseAuthMode,
    check_finalize_managed_realtime_auth_response_auth_mode,
)

T = TypeVar("T", bound="FinalizeManagedRealtimeAuthResponse")


@_attrs_define
class FinalizeManagedRealtimeAuthResponse:
    """Metadata after the predecessor static bearer credential is revoked."""

    endpoint_id: UUID
    auth_mode: FinalizeManagedRealtimeAuthResponseAuthMode
    previous_token_expires_at: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        auth_mode: str = self.auth_mode

        previous_token_expires_at: None | str
        if isinstance(self.previous_token_expires_at, datetime.datetime):
            previous_token_expires_at = self.previous_token_expires_at.isoformat()
        else:
            previous_token_expires_at = self.previous_token_expires_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "auth_mode": auth_mode,
                "previous_token_expires_at": previous_token_expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        auth_mode = check_finalize_managed_realtime_auth_response_auth_mode(d.pop("auth_mode"))

        def _parse_previous_token_expires_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                previous_token_expires_at_type_0 = datetime.datetime.fromisoformat(data)

                return previous_token_expires_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        previous_token_expires_at = _parse_previous_token_expires_at(d.pop("previous_token_expires_at"))

        finalize_managed_realtime_auth_response = cls(
            endpoint_id=endpoint_id,
            auth_mode=auth_mode,
            previous_token_expires_at=previous_token_expires_at,
        )

        finalize_managed_realtime_auth_response.additional_properties = d
        return finalize_managed_realtime_auth_response

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
