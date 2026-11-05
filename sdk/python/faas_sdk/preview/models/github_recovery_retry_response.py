from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.github_recovery_retry_response_kind import (
    GithubRecoveryRetryResponseKind,
    check_github_recovery_retry_response_kind,
)

T = TypeVar("T", bound="GithubRecoveryRetryResponse")


@_attrs_define
class GithubRecoveryRetryResponse:
    """Confirmation that one recovery item moved from dead back to pending."""

    ok: bool
    kind: GithubRecoveryRetryResponseKind
    target_id: UUID
    status: Literal["pending"]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ok = self.ok

        kind: str = self.kind

        target_id = str(self.target_id)

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "ok": ok,
                "kind": kind,
                "target_id": target_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ok = d.pop("ok")

        kind = check_github_recovery_retry_response_kind(d.pop("kind"))

        target_id = UUID(d.pop("target_id"))

        status = cast(Literal["pending"], d.pop("status"))
        if status != "pending":
            raise ValueError(f"status must match const 'pending', got '{status}'")

        github_recovery_retry_response = cls(
            ok=ok,
            kind=kind,
            target_id=target_id,
            status=status,
        )

        github_recovery_retry_response.additional_properties = d
        return github_recovery_retry_response

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
