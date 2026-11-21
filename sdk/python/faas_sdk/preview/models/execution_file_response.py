from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.execution_file_response_status import ExecutionFileResponseStatus, check_execution_file_response_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionFileResponse")


@_attrs_define
class ExecutionFileResponse:
    """File locator, publication status and integrity metadata visible to the caller."""

    id: UUID
    status: ExecutionFileResponseStatus
    size_bytes: int
    content_url: str
    """Relative authenticated content endpoint"""
    sha256: str | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    upload_required: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        size_bytes = self.size_bytes

        content_url = self.content_url

        sha256 = self.sha256

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        upload_required = self.upload_required

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "status": status,
                "size_bytes": size_bytes,
                "content_url": content_url,
            }
        )
        if sha256 is not UNSET:
            field_dict["sha256"] = sha256
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if upload_required is not UNSET:
            field_dict["upload_required"] = upload_required

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_execution_file_response_status(d.pop("status"))

        size_bytes = d.pop("size_bytes")

        content_url = d.pop("content_url")

        sha256 = d.pop("sha256", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        upload_required = d.pop("upload_required", UNSET)

        execution_file_response = cls(
            id=id,
            status=status,
            size_bytes=size_bytes,
            content_url=content_url,
            sha256=sha256,
            expires_at=expires_at,
            upload_required=upload_required,
        )

        return execution_file_response
