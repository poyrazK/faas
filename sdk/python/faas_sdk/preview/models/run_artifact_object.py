from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="RunArtifactObject")


@_attrs_define
class RunArtifactObject:
    """App-owned object locator governed by the destination bucket retention and access policy."""

    bucket_id: UUID
    key: str
    version_id: str | Unset = UNSET
    """Immutable public version when the backend supplies one. Unversioned objects retain normal app-owned mutation
    and lifecycle semantics."""

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        key = self.key

        version_id = self.version_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_id": bucket_id,
                "key": key,
            }
        )
        if version_id is not UNSET:
            field_dict["version_id"] = version_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        key = d.pop("key")

        version_id = d.pop("version_id", UNSET)

        run_artifact_object = cls(
            bucket_id=bucket_id,
            key=key,
            version_id=version_id,
        )

        return run_artifact_object
