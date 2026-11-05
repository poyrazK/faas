from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="RevokeExecutionArtifactGrantResponse")


@_attrs_define
class RevokeExecutionArtifactGrantResponse:
    """Confirmation that an artifact grant is revoked."""

    id: UUID
    revoked_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        revoked_at = self.revoked_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "revoked_at": revoked_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        revoked_at = datetime.datetime.fromisoformat(d.pop("revoked_at"))

        revoke_execution_artifact_grant_response = cls(
            id=id,
            revoked_at=revoked_at,
        )

        return revoke_execution_artifact_grant_response
