from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.agent_connection_response_approvals_item import (
    AgentConnectionResponseApprovalsItem,
    check_agent_connection_response_approvals_item,
)
from ..models.agent_connection_response_kind import AgentConnectionResponseKind, check_agent_connection_response_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentConnectionResponse")


@_attrs_define
class AgentConnectionResponse:
    """One agent connected to the account. `id` is the API key the agent uses; revoking the connection revokes that key.

    Example:
        {'id': '3f2c9a7e-1b4d-4c8e-9f0a-6d5e4c3b2a19', 'kind': 'hosted_mcp', 'client_name': 'Claude', 'client_site':
            'claude.ai', 'label': 'mcp-login', 'prefix': 'fp_live_ab12cd34', 'scopes': ['apps:read'], 'created_at':
            '2026-10-30T11:25:00Z', 'last_used_at': '2026-10-30T12:05:00Z', 'expires_at': '2026-11-06T12:05:00Z'}

    """

    id: str
    """The agent's API key id."""
    kind: AgentConnectionResponseKind
    """`hosted_mcp` for a browser sign-in through the hosted MCP server; `agent_key` for an API key with the
    `agent` scope."""
    prefix: str
    """The key's display prefix."""
    scopes: list[str]
    created_at: datetime.datetime
    app_id: str | Unset = UNSET
    """Immutable app boundary of an app_agent connection."""
    client_name: str | Unset = UNSET
    """The client name a hosted sign-in's consent page showed."""
    client_site: str | Unset = UNSET
    """The host the client was verified at: the site serving its metadata document, or its HTTPS redirect host.
    Empty for a local client."""
    label: str | Unset = UNSET
    """The key label."""
    last_used_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    org_id: UUID | Unset = UNSET
    """The organization the agent acts in."""
    member: str | Unset = UNSET
    """Email of the member who connected the agent, in an organization's list."""
    projects: list[str] | Unset = UNSET
    """Slugs of the projects the agent is limited to; absent when it reaches every project."""
    approvals: list[AgentConnectionResponseApprovalsItem] | Unset = UNSET
    """Kinds of action the agent must have a person approve, including its organization's floor."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        kind: str = self.kind

        prefix = self.prefix

        scopes = self.scopes

        created_at = self.created_at.isoformat()

        app_id = self.app_id

        client_name = self.client_name

        client_site = self.client_site

        label = self.label

        last_used_at: str | Unset = UNSET
        if not isinstance(self.last_used_at, Unset):
            last_used_at = self.last_used_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        org_id: str | Unset = UNSET
        if not isinstance(self.org_id, Unset):
            org_id = str(self.org_id)

        member = self.member

        projects: list[str] | Unset = UNSET
        if not isinstance(self.projects, Unset):
            projects = self.projects

        approvals: list[str] | Unset = UNSET
        if not isinstance(self.approvals, Unset):
            approvals = []
            for approvals_item_data in self.approvals:
                approvals_item: str = approvals_item_data
                approvals.append(approvals_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "prefix": prefix,
                "scopes": scopes,
                "created_at": created_at,
            }
        )
        if app_id is not UNSET:
            field_dict["app_id"] = app_id
        if client_name is not UNSET:
            field_dict["client_name"] = client_name
        if client_site is not UNSET:
            field_dict["client_site"] = client_site
        if label is not UNSET:
            field_dict["label"] = label
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if org_id is not UNSET:
            field_dict["org_id"] = org_id
        if member is not UNSET:
            field_dict["member"] = member
        if projects is not UNSET:
            field_dict["projects"] = projects
        if approvals is not UNSET:
            field_dict["approvals"] = approvals

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        kind = check_agent_connection_response_kind(d.pop("kind"))

        prefix = d.pop("prefix")

        scopes = cast(list[str], d.pop("scopes"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        app_id = d.pop("app_id", UNSET)

        client_name = d.pop("client_name", UNSET)

        client_site = d.pop("client_site", UNSET)

        label = d.pop("label", UNSET)

        _last_used_at = d.pop("last_used_at", UNSET)
        last_used_at: datetime.datetime | Unset
        if isinstance(_last_used_at, Unset):
            last_used_at = UNSET
        else:
            last_used_at = datetime.datetime.fromisoformat(_last_used_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _org_id = d.pop("org_id", UNSET)
        org_id: UUID | Unset
        if isinstance(_org_id, Unset):
            org_id = UNSET
        else:
            org_id = UUID(_org_id)

        member = d.pop("member", UNSET)

        projects = cast(list[str], d.pop("projects", UNSET))

        _approvals = d.pop("approvals", UNSET)
        approvals: list[AgentConnectionResponseApprovalsItem] | Unset = UNSET
        if _approvals is not UNSET:
            approvals = []
            for approvals_item_data in _approvals:
                approvals_item = check_agent_connection_response_approvals_item(approvals_item_data)

                approvals.append(approvals_item)

        agent_connection_response = cls(
            id=id,
            kind=kind,
            prefix=prefix,
            scopes=scopes,
            created_at=created_at,
            app_id=app_id,
            client_name=client_name,
            client_site=client_site,
            label=label,
            last_used_at=last_used_at,
            expires_at=expires_at,
            org_id=org_id,
            member=member,
            projects=projects,
            approvals=approvals,
        )

        agent_connection_response.additional_properties = d
        return agent_connection_response

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
