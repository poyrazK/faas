from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeWorkerCredentialRotationRequest")


@_attrs_define
class RealtimeWorkerCredentialRotationRequest:
    """Realtime Worker Credential Rotation Request."""

    ttl_seconds: int | Unset = 86400
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ttl_seconds = d.pop("ttl_seconds", UNSET)

        realtime_worker_credential_rotation_request = cls(
            ttl_seconds=ttl_seconds,
        )

        realtime_worker_credential_rotation_request.additional_properties = d
        return realtime_worker_credential_rotation_request

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
