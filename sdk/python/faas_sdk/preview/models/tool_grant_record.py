from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.tool_grant_credential import ToolGrantCredential
    from ..models.tool_grant_intent import ToolGrantIntent


T = TypeVar("T", bound="ToolGrantRecord")


@_attrs_define
class ToolGrantRecord:
    """Owner-readable retained delegation with frozen application/project identities, optional credential binding and
    irreversible revocation.

    """

    id: UUID
    account_id: str
    app: str
    app_id: str
    intent: ToolGrantIntent
    """Exact immutable delegation to an existing agent key. Expiry uses microsecond precision; discovery,
    invocation consent and single-dispatch authority are checked separately."""
    created_at: datetime.datetime
    credential: ToolGrantCredential | Unset = UNSET
    """Owner-readable immutable credential binding; contains no plaintext token. Required scopes follow the
    existing 4-KiB review metadata bound."""
    project_id: str | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = self.account_id

        app = self.app

        app_id = self.app_id

        intent = self.intent.to_dict()

        created_at = self.created_at.isoformat()

        credential: dict[str, Any] | Unset = UNSET
        if not isinstance(self.credential, Unset):
            credential = self.credential.to_dict()

        project_id = self.project_id

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app": app,
                "app_id": app_id,
                "intent": intent,
                "created_at": created_at,
            }
        )
        if credential is not UNSET:
            field_dict["credential"] = credential
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_grant_credential import ToolGrantCredential
        from ..models.tool_grant_intent import ToolGrantIntent

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = d.pop("account_id")

        app = d.pop("app")

        app_id = d.pop("app_id")

        intent = ToolGrantIntent.from_dict(d.pop("intent"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _credential = d.pop("credential", UNSET)
        credential: ToolGrantCredential | Unset
        if isinstance(_credential, Unset):
            credential = UNSET
        else:
            credential = ToolGrantCredential.from_dict(_credential)

        project_id = d.pop("project_id", UNSET)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        tool_grant_record = cls(
            id=id,
            account_id=account_id,
            app=app,
            app_id=app_id,
            intent=intent,
            created_at=created_at,
            credential=credential,
            project_id=project_id,
            revoked_at=revoked_at,
        )

        tool_grant_record.additional_properties = d
        return tool_grant_record

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
