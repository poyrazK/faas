from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.run_artifact_delivery_status import RunArtifactDeliveryStatus, check_run_artifact_delivery_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.run_artifact_object import RunArtifactObject


T = TypeVar("T", bound="RunArtifactDelivery")


@_attrs_define
class RunArtifactDelivery:
    """Durable storage write receipt independent of source Run file retention and Agent thread lifetime."""

    id: UUID
    status: RunArtifactDeliveryStatus
    size_bytes: int
    content_type: str
    receipt_url: str
    """Authenticated app-owned receipt locator"""
    created_at: datetime.datetime
    error_code: str | Unset = UNSET
    object_: RunArtifactObject | Unset = UNSET
    """App-owned object locator governed by the destination bucket retention and access policy."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        size_bytes = self.size_bytes

        content_type = self.content_type

        receipt_url = self.receipt_url

        created_at = self.created_at.isoformat()

        error_code = self.error_code

        object_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.object_, Unset):
            object_ = self.object_.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "status": status,
                "size_bytes": size_bytes,
                "content_type": content_type,
                "receipt_url": receipt_url,
                "created_at": created_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if object_ is not UNSET:
            field_dict["object"] = object_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.run_artifact_object import RunArtifactObject

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_run_artifact_delivery_status(d.pop("status"))

        size_bytes = d.pop("size_bytes")

        content_type = d.pop("content_type")

        receipt_url = d.pop("receipt_url")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        error_code = d.pop("error_code", UNSET)

        _object_ = d.pop("object", UNSET)
        object_: RunArtifactObject | Unset
        if isinstance(_object_, Unset):
            object_ = UNSET
        else:
            object_ = RunArtifactObject.from_dict(_object_)

        run_artifact_delivery = cls(
            id=id,
            status=status,
            size_bytes=size_bytes,
            content_type=content_type,
            receipt_url=receipt_url,
            created_at=created_at,
            error_code=error_code,
            object_=object_,
        )

        return run_artifact_delivery
