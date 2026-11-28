from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.object_signed_request import ObjectSignedRequest


T = TypeVar("T", bound="JobArtifactDownloadResponse")


@_attrs_define
class JobArtifactDownloadResponse:
    """Verified managed artifact and short-lived signed download request."""

    name: str
    size_bytes: int
    sha256: str
    verified_at: datetime.datetime
    download: ObjectSignedRequest
    """Temporary bearer capability. Bucket object GET/HEAD/PUT URLs use the branded S3 gateway; a PUT URL
    dispatches at most one write. Multipart part URLs retain their separate contract. Do not log or persist URLs.
   """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        verified_at = self.verified_at.isoformat()

        download = self.download.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "verified_at": verified_at,
                "download": download,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_signed_request import ObjectSignedRequest

        d = dict(src_dict)
        name = d.pop("name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        verified_at = datetime.datetime.fromisoformat(d.pop("verified_at"))

        download = ObjectSignedRequest.from_dict(d.pop("download"))

        job_artifact_download_response = cls(
            name=name,
            size_bytes=size_bytes,
            sha256=sha256,
            verified_at=verified_at,
            download=download,
        )

        job_artifact_download_response.additional_properties = d
        return job_artifact_download_response

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
