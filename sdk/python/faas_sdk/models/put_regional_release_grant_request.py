from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="PutRegionalReleaseGrantRequest")


@_attrs_define
class PutRegionalReleaseGrantRequest:
    """Immutable regional stream delegation; the plaintext token never reaches registration."""

    global_app_id: UUID
    """Global image release stream UUID."""
    token_sha256: str
    """SHA-256 of the directory-minted fp_release_ bearer."""
    expires_at: datetime.datetime
    """Fixed expiry within seven days; canonicalized to UTC microseconds."""

    def to_dict(self) -> dict[str, Any]:
        global_app_id = str(self.global_app_id)

        token_sha256 = self.token_sha256

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "global_app_id": global_app_id,
                "token_sha256": token_sha256,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        global_app_id = UUID(d.pop("global_app_id"))

        token_sha256 = d.pop("token_sha256")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        put_regional_release_grant_request = cls(
            global_app_id=global_app_id,
            token_sha256=token_sha256,
            expires_at=expires_at,
        )

        return put_regional_release_grant_request
