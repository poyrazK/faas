from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataAPIRuntime")


@_attrs_define
class DataAPIRuntime:
    """The Data API runtime the control plane serves (ADR-20261031095954)."""

    version: str
    """Digest of the runtime's image files; running Data APIs report it as runtime_version on /healthz."""
    archive_sha256: str
    """Lowercase hex SHA-256 of the decoded archive."""
    archive: str
    """Base64 gzipped tar of the runtime under one data-api/ directory, at most 2 MiB decoded."""

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        archive_sha256 = self.archive_sha256

        archive = self.archive

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "archive_sha256": archive_sha256,
                "archive": archive,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        archive_sha256 = d.pop("archive_sha256")

        archive = d.pop("archive")

        data_api_runtime = cls(
            version=version,
            archive_sha256=archive_sha256,
            archive=archive,
        )

        return data_api_runtime
