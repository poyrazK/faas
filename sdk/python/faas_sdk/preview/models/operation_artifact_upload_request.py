from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationArtifactUploadRequest")


@_attrs_define
class OperationArtifactUploadRequest:
    """Direct private file declaration. Gregale derives its opaque operation reference and storage key."""

    report_id: str
    name: str
    size_bytes: int
    sha256: str

    def to_dict(self) -> dict[str, Any]:
        report_id = self.report_id

        name = self.name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "report_id": report_id,
                "name": name,
                "size_bytes": size_bytes,
                "sha256": sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        report_id = d.pop("report_id")

        name = d.pop("name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        operation_artifact_upload_request = cls(
            report_id=report_id,
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
        )

        return operation_artifact_upload_request
