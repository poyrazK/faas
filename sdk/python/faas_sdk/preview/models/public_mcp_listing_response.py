from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.consumer_signup_plan import ConsumerSignupPlan
    from ..models.mcp_contract_changelog_entry import MCPContractChangelogEntry
    from ..models.mcp_listing_install import MCPListingInstall
    from ..models.mcp_listing_tool import MCPListingTool


T = TypeVar("T", bound="PublicMCPListingResponse")


@_attrs_define
class PublicMCPListingResponse:
    """A sold MCP server's public listing."""

    app: str
    title: str
    summary: str
    """What the listed server does."""
    endpoint: str
    tools: list[MCPListingTool]
    plans: list[ConsumerSignupPlan]
    livemode: bool
    signup_open: bool
    install: MCPListingInstall
    """Ways to add a listed MCP server to a client."""
    published_at: datetime.datetime
    trial_days: int | Unset = UNSET
    signup_url: str | Unset = UNSET
    """The sign-up page, while sign-up is open."""
    customers: int | Unset = UNSET
    """Open customers, when the seller shows usage."""
    changelog: list[MCPContractChangelogEntry] | Unset = UNSET
    """Tool contract changes shipped since the listing was published, newest first."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        title = self.title

        summary = self.summary

        endpoint = self.endpoint

        tools = []
        for tools_item_data in self.tools:
            tools_item = tools_item_data.to_dict()
            tools.append(tools_item)

        plans = []
        for plans_item_data in self.plans:
            plans_item = plans_item_data.to_dict()
            plans.append(plans_item)

        livemode = self.livemode

        signup_open = self.signup_open

        install = self.install.to_dict()

        published_at = self.published_at.isoformat()

        trial_days = self.trial_days

        signup_url = self.signup_url

        customers = self.customers

        changelog: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.changelog, Unset):
            changelog = []
            for changelog_item_data in self.changelog:
                changelog_item = changelog_item_data.to_dict()
                changelog.append(changelog_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "title": title,
                "summary": summary,
                "endpoint": endpoint,
                "tools": tools,
                "plans": plans,
                "livemode": livemode,
                "signup_open": signup_open,
                "install": install,
                "published_at": published_at,
            }
        )
        if trial_days is not UNSET:
            field_dict["trial_days"] = trial_days
        if signup_url is not UNSET:
            field_dict["signup_url"] = signup_url
        if customers is not UNSET:
            field_dict["customers"] = customers
        if changelog is not UNSET:
            field_dict["changelog"] = changelog

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_signup_plan import ConsumerSignupPlan
        from ..models.mcp_contract_changelog_entry import MCPContractChangelogEntry
        from ..models.mcp_listing_install import MCPListingInstall
        from ..models.mcp_listing_tool import MCPListingTool

        d = dict(src_dict)
        app = d.pop("app")

        title = d.pop("title")

        summary = d.pop("summary")

        endpoint = d.pop("endpoint")

        tools = []
        _tools = d.pop("tools")
        for tools_item_data in _tools:
            tools_item = MCPListingTool.from_dict(tools_item_data)

            tools.append(tools_item)

        plans = []
        _plans = d.pop("plans")
        for plans_item_data in _plans:
            plans_item = ConsumerSignupPlan.from_dict(plans_item_data)

            plans.append(plans_item)

        livemode = d.pop("livemode")

        signup_open = d.pop("signup_open")

        install = MCPListingInstall.from_dict(d.pop("install"))

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        trial_days = d.pop("trial_days", UNSET)

        signup_url = d.pop("signup_url", UNSET)

        customers = d.pop("customers", UNSET)

        _changelog = d.pop("changelog", UNSET)
        changelog: list[MCPContractChangelogEntry] | Unset = UNSET
        if _changelog is not UNSET:
            changelog = []
            for changelog_item_data in _changelog:
                changelog_item = MCPContractChangelogEntry.from_dict(changelog_item_data)

                changelog.append(changelog_item)

        public_mcp_listing_response = cls(
            app=app,
            title=title,
            summary=summary,
            endpoint=endpoint,
            tools=tools,
            plans=plans,
            livemode=livemode,
            signup_open=signup_open,
            install=install,
            published_at=published_at,
            trial_days=trial_days,
            signup_url=signup_url,
            customers=customers,
            changelog=changelog,
        )

        public_mcp_listing_response.additional_properties = d
        return public_mcp_listing_response

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
