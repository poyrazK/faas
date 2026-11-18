from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deleted_app_purge_response_pending_reason import (
    DeletedAppPurgeResponsePendingReason,
    check_deleted_app_purge_response_pending_reason,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DeletedAppPurgeResponse")


@_attrs_define
class DeletedAppPurgeResponse:
    """Outcome of an owner's purge. The restore window is closed either way."""

    account_id: UUID
    id: UUID
    slug: str
    purged: bool
    """True once the app is gone and its slug is free for this account."""
    pending_reason: DeletedAppPurgeResponsePendingReason | Unset = UNSET
    """What still holds the app while purged is false; the grace sweep retries."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        id = str(self.id)

        slug = self.slug

        purged = self.purged

        pending_reason: str | Unset = UNSET
        if not isinstance(self.pending_reason, Unset):
            pending_reason = self.pending_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "id": id,
                "slug": slug,
                "purged": purged,
            }
        )
        if pending_reason is not UNSET:
            field_dict["pending_reason"] = pending_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        id = UUID(d.pop("id"))

        slug = d.pop("slug")

        purged = d.pop("purged")

        _pending_reason = d.pop("pending_reason", UNSET)
        pending_reason: DeletedAppPurgeResponsePendingReason | Unset
        if isinstance(_pending_reason, Unset):
            pending_reason = UNSET
        else:
            pending_reason = check_deleted_app_purge_response_pending_reason(_pending_reason)

        deleted_app_purge_response = cls(
            account_id=account_id,
            id=id,
            slug=slug,
            purged=purged,
            pending_reason=pending_reason,
        )

        deleted_app_purge_response.additional_properties = d
        return deleted_app_purge_response

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
