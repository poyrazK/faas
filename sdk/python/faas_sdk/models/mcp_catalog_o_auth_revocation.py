from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_revocation_failure_reason import (
    MCPCatalogOAuthRevocationFailureReason,
    check_mcp_catalog_o_auth_revocation_failure_reason,
)
from ..models.mcp_catalog_o_auth_revocation_state import (
    MCPCatalogOAuthRevocationState,
    check_mcp_catalog_o_auth_revocation_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_observer_o_auth_config import MCPCatalogObserverOAuthConfig


T = TypeVar("T", bound="MCPCatalogOAuthRevocation")


@_attrs_define
class MCPCatalogOAuthRevocation:
    """Durable RFC 7009 progress; acknowledgement also covers tokens already invalid at the provider. Private fields are
    omitted from all public projections.

    """

    id: UUID
    state: MCPCatalogOAuthRevocationState
    attempts: int
    next_attempt_at: datetime.datetime
    refresh_acknowledged: bool
    access_acknowledged: bool
    failure_reason: MCPCatalogOAuthRevocationFailureReason | Unset = UNSET
    acknowledged_at: datetime.datetime | Unset = UNSET
    private_config: MCPCatalogObserverOAuthConfig | Unset = UNSET
    """Explicit OAuth refresh grant for one app and role; credentials are app secret references in the connection
    environment."""
    private_client_ciphertext: str | Unset = UNSET
    """Internal sealed client credential captured at disconnect and omitted from responses"""
    private_ciphertext: str | Unset = UNSET
    """Internal sealed retired grant omitted from responses"""
    private_lease_id: str | Unset = UNSET
    """Internal fence omitted from responses"""
    private_lease_until: datetime.datetime | Unset = UNSET
    """Internal lease omitted from responses"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        state: str = self.state

        attempts = self.attempts

        next_attempt_at = self.next_attempt_at.isoformat()

        refresh_acknowledged = self.refresh_acknowledged

        access_acknowledged = self.access_acknowledged

        failure_reason: str | Unset = UNSET
        if not isinstance(self.failure_reason, Unset):
            failure_reason = self.failure_reason

        acknowledged_at: str | Unset = UNSET
        if not isinstance(self.acknowledged_at, Unset):
            acknowledged_at = self.acknowledged_at.isoformat()

        private_config: dict[str, Any] | Unset = UNSET
        if not isinstance(self.private_config, Unset):
            private_config = self.private_config.to_dict()

        private_client_ciphertext = self.private_client_ciphertext

        private_ciphertext = self.private_ciphertext

        private_lease_id = self.private_lease_id

        private_lease_until: str | Unset = UNSET
        if not isinstance(self.private_lease_until, Unset):
            private_lease_until = self.private_lease_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "state": state,
                "attempts": attempts,
                "next_attempt_at": next_attempt_at,
                "refresh_acknowledged": refresh_acknowledged,
                "access_acknowledged": access_acknowledged,
            }
        )
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if private_config is not UNSET:
            field_dict["private_config"] = private_config
        if private_client_ciphertext is not UNSET:
            field_dict["private_client_ciphertext"] = private_client_ciphertext
        if private_ciphertext is not UNSET:
            field_dict["private_ciphertext"] = private_ciphertext
        if private_lease_id is not UNSET:
            field_dict["private_lease_id"] = private_lease_id
        if private_lease_until is not UNSET:
            field_dict["private_lease_until"] = private_lease_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_observer_o_auth_config import MCPCatalogObserverOAuthConfig

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        state = check_mcp_catalog_o_auth_revocation_state(d.pop("state"))

        attempts = d.pop("attempts")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        refresh_acknowledged = d.pop("refresh_acknowledged")

        access_acknowledged = d.pop("access_acknowledged")

        _failure_reason = d.pop("failure_reason", UNSET)
        failure_reason: MCPCatalogOAuthRevocationFailureReason | Unset
        if isinstance(_failure_reason, Unset):
            failure_reason = UNSET
        else:
            failure_reason = check_mcp_catalog_o_auth_revocation_failure_reason(_failure_reason)

        _acknowledged_at = d.pop("acknowledged_at", UNSET)
        acknowledged_at: datetime.datetime | Unset
        if isinstance(_acknowledged_at, Unset):
            acknowledged_at = UNSET
        else:
            acknowledged_at = datetime.datetime.fromisoformat(_acknowledged_at)

        _private_config = d.pop("private_config", UNSET)
        private_config: MCPCatalogObserverOAuthConfig | Unset
        if isinstance(_private_config, Unset):
            private_config = UNSET
        else:
            private_config = MCPCatalogObserverOAuthConfig.from_dict(_private_config)

        private_client_ciphertext = d.pop("private_client_ciphertext", UNSET)

        private_ciphertext = d.pop("private_ciphertext", UNSET)

        private_lease_id = d.pop("private_lease_id", UNSET)

        _private_lease_until = d.pop("private_lease_until", UNSET)
        private_lease_until: datetime.datetime | Unset
        if isinstance(_private_lease_until, Unset):
            private_lease_until = UNSET
        else:
            private_lease_until = datetime.datetime.fromisoformat(_private_lease_until)

        mcp_catalog_o_auth_revocation = cls(
            id=id,
            state=state,
            attempts=attempts,
            next_attempt_at=next_attempt_at,
            refresh_acknowledged=refresh_acknowledged,
            access_acknowledged=access_acknowledged,
            failure_reason=failure_reason,
            acknowledged_at=acknowledged_at,
            private_config=private_config,
            private_client_ciphertext=private_client_ciphertext,
            private_ciphertext=private_ciphertext,
            private_lease_id=private_lease_id,
            private_lease_until=private_lease_until,
        )

        mcp_catalog_o_auth_revocation.additional_properties = d
        return mcp_catalog_o_auth_revocation

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
