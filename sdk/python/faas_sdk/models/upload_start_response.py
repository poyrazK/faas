from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UploadStartResponse")


@_attrs_define
class UploadStartResponse:
    """Body of POST /v1/uploads response. The session row persists for 24h; the reaper (cmd/apid/upload_session_reaper.go)
    flips `status='open'` rows whose `expires_at` has passed to 'expired' on a 5-min ticker. `chunk_size` is server-
    decided (8 MiB default; 16 MiB for Scale).

    """

    upload_id: str
    chunk_size: int
    """Server-decided chunk size in bytes. Default 8 MiB; 16 MiB for Scale."""
    total_size: int
    """Echo of the requested total_size for client confirmation."""
    expires_at: datetime.datetime
    """ISO 8601 UTC timestamp; PATCH after this returns 410 Gone."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        upload_id = self.upload_id

        chunk_size = self.chunk_size

        total_size = self.total_size

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "upload_id": upload_id,
                "chunk_size": chunk_size,
                "total_size": total_size,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        upload_id = d.pop("upload_id")

        chunk_size = d.pop("chunk_size")

        total_size = d.pop("total_size")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        upload_start_response = cls(
            upload_id=upload_id,
            chunk_size=chunk_size,
            total_size=total_size,
            expires_at=expires_at,
        )

        upload_start_response.additional_properties = d
        return upload_start_response

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
