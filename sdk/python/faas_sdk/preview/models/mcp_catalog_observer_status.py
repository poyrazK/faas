from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_observer_status_failure_reason import (
    MCPCatalogObserverStatusFailureReason,
    check_mcp_catalog_observer_status_failure_reason,
)
from ..models.mcp_catalog_observer_status_state import (
    MCPCatalogObserverStatusState,
    check_mcp_catalog_observer_status_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_catalog_o_auth_flow import MCPCatalogOAuthFlow
    from ..models.mcp_catalog_o_auth_renewal import MCPCatalogOAuthRenewal
    from ..models.mcp_catalog_observer_o_auth_status import MCPCatalogObserverOAuthStatus


T = TypeVar("T", bound="MCPCatalogObserverStatus")


@_attrs_define
class MCPCatalogObserverStatus:
    """Managed observer scheduling and recovery metadata without credential values or raw source errors."""

    state: MCPCatalogObserverStatusState
    consecutive_failures: int
    last_attempt_at: datetime.datetime | Unset = UNSET
    last_success_at: datetime.datetime | Unset = UNSET
    next_check_at: datetime.datetime | Unset = UNSET
    failure_reason: MCPCatalogObserverStatusFailureReason | Unset = UNSET
    lease_id: UUID | Unset = UNSET
    """Internal fencing metadata omitted from public monitor projections"""
    lease_until: datetime.datetime | Unset = UNSET
    oauth: MCPCatalogObserverOAuthStatus | Unset = UNSET
    """Public OAuth connection health; sealed credentials and source fingerprint are omitted from responses."""
    oauth_renewal: MCPCatalogOAuthRenewal | Unset = UNSET
    """Renewal recovery metadata. Private operation fields and sealed results are omitted from public responses."""
    oauth_flow: MCPCatalogOAuthFlow | Unset = UNSET
    """Internal sealed one-time authorization state omitted from public monitor projections."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        consecutive_failures = self.consecutive_failures

        last_attempt_at: str | Unset = UNSET
        if not isinstance(self.last_attempt_at, Unset):
            last_attempt_at = self.last_attempt_at.isoformat()

        last_success_at: str | Unset = UNSET
        if not isinstance(self.last_success_at, Unset):
            last_success_at = self.last_success_at.isoformat()

        next_check_at: str | Unset = UNSET
        if not isinstance(self.next_check_at, Unset):
            next_check_at = self.next_check_at.isoformat()

        failure_reason: str | Unset = UNSET
        if not isinstance(self.failure_reason, Unset):
            failure_reason = self.failure_reason

        lease_id: str | Unset = UNSET
        if not isinstance(self.lease_id, Unset):
            lease_id = str(self.lease_id)

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        oauth: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth, Unset):
            oauth = self.oauth.to_dict()

        oauth_renewal: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_renewal, Unset):
            oauth_renewal = self.oauth_renewal.to_dict()

        oauth_flow: dict[str, Any] | Unset = UNSET
        if not isinstance(self.oauth_flow, Unset):
            oauth_flow = self.oauth_flow.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
                "consecutive_failures": consecutive_failures,
            }
        )
        if last_attempt_at is not UNSET:
            field_dict["last_attempt_at"] = last_attempt_at
        if last_success_at is not UNSET:
            field_dict["last_success_at"] = last_success_at
        if next_check_at is not UNSET:
            field_dict["next_check_at"] = next_check_at
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason
        if lease_id is not UNSET:
            field_dict["lease_id"] = lease_id
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until
        if oauth is not UNSET:
            field_dict["oauth"] = oauth
        if oauth_renewal is not UNSET:
            field_dict["oauth_renewal"] = oauth_renewal
        if oauth_flow is not UNSET:
            field_dict["oauth_flow"] = oauth_flow

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_catalog_o_auth_flow import MCPCatalogOAuthFlow
        from ..models.mcp_catalog_o_auth_renewal import MCPCatalogOAuthRenewal
        from ..models.mcp_catalog_observer_o_auth_status import MCPCatalogObserverOAuthStatus

        d = dict(src_dict)
        state = check_mcp_catalog_observer_status_state(d.pop("state"))

        consecutive_failures = d.pop("consecutive_failures")

        _last_attempt_at = d.pop("last_attempt_at", UNSET)
        last_attempt_at: datetime.datetime | Unset
        if isinstance(_last_attempt_at, Unset):
            last_attempt_at = UNSET
        else:
            last_attempt_at = datetime.datetime.fromisoformat(_last_attempt_at)

        _last_success_at = d.pop("last_success_at", UNSET)
        last_success_at: datetime.datetime | Unset
        if isinstance(_last_success_at, Unset):
            last_success_at = UNSET
        else:
            last_success_at = datetime.datetime.fromisoformat(_last_success_at)

        _next_check_at = d.pop("next_check_at", UNSET)
        next_check_at: datetime.datetime | Unset
        if isinstance(_next_check_at, Unset):
            next_check_at = UNSET
        else:
            next_check_at = datetime.datetime.fromisoformat(_next_check_at)

        _failure_reason = d.pop("failure_reason", UNSET)
        failure_reason: MCPCatalogObserverStatusFailureReason | Unset
        if isinstance(_failure_reason, Unset):
            failure_reason = UNSET
        else:
            failure_reason = check_mcp_catalog_observer_status_failure_reason(_failure_reason)

        _lease_id = d.pop("lease_id", UNSET)
        lease_id: UUID | Unset
        if isinstance(_lease_id, Unset):
            lease_id = UNSET
        else:
            lease_id = UUID(_lease_id)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        _oauth = d.pop("oauth", UNSET)
        oauth: MCPCatalogObserverOAuthStatus | Unset
        if isinstance(_oauth, Unset):
            oauth = UNSET
        else:
            oauth = MCPCatalogObserverOAuthStatus.from_dict(_oauth)

        _oauth_renewal = d.pop("oauth_renewal", UNSET)
        oauth_renewal: MCPCatalogOAuthRenewal | Unset
        if isinstance(_oauth_renewal, Unset):
            oauth_renewal = UNSET
        else:
            oauth_renewal = MCPCatalogOAuthRenewal.from_dict(_oauth_renewal)

        _oauth_flow = d.pop("oauth_flow", UNSET)
        oauth_flow: MCPCatalogOAuthFlow | Unset
        if isinstance(_oauth_flow, Unset):
            oauth_flow = UNSET
        else:
            oauth_flow = MCPCatalogOAuthFlow.from_dict(_oauth_flow)

        mcp_catalog_observer_status = cls(
            state=state,
            consecutive_failures=consecutive_failures,
            last_attempt_at=last_attempt_at,
            last_success_at=last_success_at,
            next_check_at=next_check_at,
            failure_reason=failure_reason,
            lease_id=lease_id,
            lease_until=lease_until,
            oauth=oauth,
            oauth_renewal=oauth_renewal,
            oauth_flow=oauth_flow,
        )

        mcp_catalog_observer_status.additional_properties = d
        return mcp_catalog_observer_status

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
