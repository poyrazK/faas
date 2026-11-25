from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.unclaimed_limits import UnclaimedLimits


T = TypeVar("T", bound="AnonymousSessionResponse")


@_attrs_define
class AnonymousSessionResponse:
    """An anonymous deploy session."""

    account_id: UUID
    api_key: str
    """Deploy key, returned once. Use it as a bearer token."""
    claim_url: str
    """Give this link to the person the agent works for."""
    expires_at: datetime.datetime
    """The account and its apps are deleted after this unless claimed."""
    limits: UnclaimedLimits
    """What an account deployed without an owner may use until it is claimed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        api_key = self.api_key

        claim_url = self.claim_url

        expires_at = self.expires_at.isoformat()

        limits = self.limits.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "api_key": api_key,
                "claim_url": claim_url,
                "expires_at": expires_at,
                "limits": limits,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.unclaimed_limits import UnclaimedLimits

        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        api_key = d.pop("api_key")

        claim_url = d.pop("claim_url")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        limits = UnclaimedLimits.from_dict(d.pop("limits"))

        anonymous_session_response = cls(
            account_id=account_id,
            api_key=api_key,
            claim_url=claim_url,
            expires_at=expires_at,
            limits=limits,
        )

        anonymous_session_response.additional_properties = d
        return anonymous_session_response

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
