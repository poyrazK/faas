from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.upload_session_response_status import UploadSessionResponseStatus, check_upload_session_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="UploadSessionResponse")


@_attrs_define
class UploadSessionResponse:
    """Current resumable upload state. The received_bytes value is the next Upload-Offset a client should use."""

    upload_id: str
    app_slug: str
    chunk_size: int
    total_size: int
    received_bytes: int
    status: UploadSessionResponseStatus
    expires_at: datetime.datetime
    deployment_id: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        upload_id = self.upload_id

        app_slug = self.app_slug

        chunk_size = self.chunk_size

        total_size = self.total_size

        received_bytes = self.received_bytes

        status: str = self.status

        expires_at = self.expires_at.isoformat()

        deployment_id: None | str | Unset
        if isinstance(self.deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = self.deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "upload_id": upload_id,
                "app_slug": app_slug,
                "chunk_size": chunk_size,
                "total_size": total_size,
                "received_bytes": received_bytes,
                "status": status,
                "expires_at": expires_at,
            }
        )
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        upload_id = d.pop("upload_id")

        app_slug = d.pop("app_slug")

        chunk_size = d.pop("chunk_size")

        total_size = d.pop("total_size")

        received_bytes = d.pop("received_bytes")

        status = check_upload_session_response_status(d.pop("status"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        def _parse_deployment_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        deployment_id = _parse_deployment_id(d.pop("deployment_id", UNSET))

        upload_session_response = cls(
            upload_id=upload_id,
            app_slug=app_slug,
            chunk_size=chunk_size,
            total_size=total_size,
            received_bytes=received_bytes,
            status=status,
            expires_at=expires_at,
            deployment_id=deployment_id,
        )

        upload_session_response.additional_properties = d
        return upload_session_response

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
