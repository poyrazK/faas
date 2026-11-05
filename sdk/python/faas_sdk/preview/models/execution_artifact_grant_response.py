from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="ExecutionArtifactGrantResponse")


@_attrs_define
class ExecutionArtifactGrantResponse:
    """One-time artifact bearer capability. The token is returned once; the server stores only its hash."""

    id: UUID
    source_execution_id: UUID
    artifact_name: str
    token: str
    """Bearer capability returned once; the server stores only its hash."""
    expires_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        source_execution_id = str(self.source_execution_id)

        artifact_name = self.artifact_name

        token = self.token

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "source_execution_id": source_execution_id,
                "artifact_name": artifact_name,
                "token": token,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        source_execution_id = UUID(d.pop("source_execution_id"))

        artifact_name = d.pop("artifact_name")

        token = d.pop("token")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        execution_artifact_grant_response = cls(
            id=id,
            source_execution_id=source_execution_id,
            artifact_name=artifact_name,
            token=token,
            expires_at=expires_at,
        )

        return execution_artifact_grant_response
