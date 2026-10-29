from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_renewal_outcome import (
    MCPCatalogOAuthRenewalOutcome,
    check_mcp_catalog_o_auth_renewal_outcome,
)
from ..models.mcp_catalog_o_auth_renewal_state import (
    MCPCatalogOAuthRenewalState,
    check_mcp_catalog_o_auth_renewal_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_observer_o_auth_status import MCPCatalogObserverOAuthStatus


T = TypeVar("T", bound="MCPCatalogOAuthRenewal")


@_attrs_define
class MCPCatalogOAuthRenewal:
    """Renewal recovery metadata. Private operation fields and sealed results are omitted from public responses."""

    state: MCPCatalogOAuthRenewalState
    started_at: datetime.datetime
    outcome: MCPCatalogOAuthRenewalOutcome | Unset = UNSET
    private_id: UUID | Unset = UNSET
    private_source_sha256: str | Unset = UNSET
    private_lease_id: UUID | Unset = UNSET
    private_result: MCPCatalogObserverOAuthStatus | Unset = UNSET
    """Public OAuth connection health; sealed credentials and source fingerprint are omitted from responses."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        started_at = self.started_at.isoformat()

        outcome: str | Unset = UNSET
        if not isinstance(self.outcome, Unset):
            outcome = self.outcome

        private_id: str | Unset = UNSET
        if not isinstance(self.private_id, Unset):
            private_id = str(self.private_id)

        private_source_sha256 = self.private_source_sha256

        private_lease_id: str | Unset = UNSET
        if not isinstance(self.private_lease_id, Unset):
            private_lease_id = str(self.private_lease_id)

        private_result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.private_result, Unset):
            private_result = self.private_result.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "started_at": started_at,
            }
        )
        if outcome is not UNSET:
            field_dict["outcome"] = outcome
        if private_id is not UNSET:
            field_dict["private_id"] = private_id
        if private_source_sha256 is not UNSET:
            field_dict["private_source_sha256"] = private_source_sha256
        if private_lease_id is not UNSET:
            field_dict["private_lease_id"] = private_lease_id
        if private_result is not UNSET:
            field_dict["private_result"] = private_result

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_observer_o_auth_status import MCPCatalogObserverOAuthStatus

        d = dict(src_dict)
        state = check_mcp_catalog_o_auth_renewal_state(d.pop("state"))

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        _outcome = d.pop("outcome", UNSET)
        outcome: MCPCatalogOAuthRenewalOutcome | Unset
        if isinstance(_outcome, Unset):
            outcome = UNSET
        else:
            outcome = check_mcp_catalog_o_auth_renewal_outcome(_outcome)

        _private_id = d.pop("private_id", UNSET)
        private_id: UUID | Unset
        if isinstance(_private_id, Unset):
            private_id = UNSET
        else:
            private_id = UUID(_private_id)

        private_source_sha256 = d.pop("private_source_sha256", UNSET)

        _private_lease_id = d.pop("private_lease_id", UNSET)
        private_lease_id: UUID | Unset
        if isinstance(_private_lease_id, Unset):
            private_lease_id = UNSET
        else:
            private_lease_id = UUID(_private_lease_id)

        _private_result = d.pop("private_result", UNSET)
        private_result: MCPCatalogObserverOAuthStatus | Unset
        if isinstance(_private_result, Unset):
            private_result = UNSET
        else:
            private_result = MCPCatalogObserverOAuthStatus.from_dict(_private_result)

        mcp_catalog_o_auth_renewal = cls(
            state=state,
            started_at=started_at,
            outcome=outcome,
            private_id=private_id,
            private_source_sha256=private_source_sha256,
            private_lease_id=private_lease_id,
            private_result=private_result,
        )

        mcp_catalog_o_auth_renewal.additional_properties = d
        return mcp_catalog_o_auth_renewal

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
