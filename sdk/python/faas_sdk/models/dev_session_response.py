from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.app_response import AppResponse


T = TypeVar("T", bound="DevSessionResponse")


@_attrs_define
class DevSessionResponse:
    """Stable remote developer workspace and its renewable lease."""

    app: AppResponse
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy
    pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169
    / #172)."""
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app.to_dict()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_response import AppResponse

        d = dict(src_dict)
        app = AppResponse.from_dict(d.pop("app"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        dev_session_response = cls(
            app=app,
            expires_at=expires_at,
        )

        dev_session_response.additional_properties = d
        return dev_session_response

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
