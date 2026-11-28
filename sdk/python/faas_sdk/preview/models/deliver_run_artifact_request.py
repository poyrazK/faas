from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="DeliverRunArtifactRequest")


@_attrs_define
class DeliverRunArtifactRequest:
    """Exact Run output and stable storage intent to promote into an application upload route."""

    idempotency_key: str
    """Stable app/route delivery identity. Reuse after an uncertain response; a new key explicitly requests a new
    storage attempt without executing code."""
    execution_id: UUID
    artifact_name: str
    size_bytes: int
    sha256: str
    content_type: str

    def to_dict(self) -> dict[str, Any]:
        idempotency_key = self.idempotency_key

        execution_id = str(self.execution_id)

        artifact_name = self.artifact_name

        size_bytes = self.size_bytes

        sha256 = self.sha256

        content_type = self.content_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "idempotency_key": idempotency_key,
                "execution_id": execution_id,
                "artifact_name": artifact_name,
                "size_bytes": size_bytes,
                "sha256": sha256,
                "content_type": content_type,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        idempotency_key = d.pop("idempotency_key")

        execution_id = UUID(d.pop("execution_id"))

        artifact_name = d.pop("artifact_name")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        content_type = d.pop("content_type")

        deliver_run_artifact_request = cls(
            idempotency_key=idempotency_key,
            execution_id=execution_id,
            artifact_name=artifact_name,
            size_bytes=size_bytes,
            sha256=sha256,
            content_type=content_type,
        )

        return deliver_run_artifact_request
