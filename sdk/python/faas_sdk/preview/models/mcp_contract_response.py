from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_contract_check import MCPContractCheck
    from ..models.mcp_contract_override import MCPContractOverride


T = TypeVar("T", bound="MCPContractResponse")


@_attrs_define
class MCPContractResponse:
    """The tool contract a listed MCP server's production deploys are held to."""

    app: str
    baseline_sha256: str
    listing_version: int
    tools: list[str]
    checks: list[MCPContractCheck]
    overrides: list[MCPContractOverride]
    baseline_deployment_id: UUID | Unset = UNSET
    """The accepted candidate the baseline comes from; absent when it is the listing's."""
    probe_secret: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        baseline_sha256 = self.baseline_sha256

        listing_version = self.listing_version

        tools = self.tools

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        overrides = []
        for overrides_item_data in self.overrides:
            overrides_item = overrides_item_data.to_dict()
            overrides.append(overrides_item)

        baseline_deployment_id: str | Unset = UNSET
        if not isinstance(self.baseline_deployment_id, Unset):
            baseline_deployment_id = str(self.baseline_deployment_id)

        probe_secret = self.probe_secret

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "baseline_sha256": baseline_sha256,
                "listing_version": listing_version,
                "tools": tools,
                "checks": checks,
                "overrides": overrides,
            }
        )
        if baseline_deployment_id is not UNSET:
            field_dict["baseline_deployment_id"] = baseline_deployment_id
        if probe_secret is not UNSET:
            field_dict["probe_secret"] = probe_secret

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_contract_check import MCPContractCheck
        from ..models.mcp_contract_override import MCPContractOverride

        d = dict(src_dict)
        app = d.pop("app")

        baseline_sha256 = d.pop("baseline_sha256")

        listing_version = d.pop("listing_version")

        tools = cast(list[str], d.pop("tools"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = MCPContractCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        overrides = []
        _overrides = d.pop("overrides")
        for overrides_item_data in _overrides:
            overrides_item = MCPContractOverride.from_dict(overrides_item_data)

            overrides.append(overrides_item)

        _baseline_deployment_id = d.pop("baseline_deployment_id", UNSET)
        baseline_deployment_id: UUID | Unset
        if isinstance(_baseline_deployment_id, Unset):
            baseline_deployment_id = UNSET
        else:
            baseline_deployment_id = UUID(_baseline_deployment_id)

        probe_secret = d.pop("probe_secret", UNSET)

        mcp_contract_response = cls(
            app=app,
            baseline_sha256=baseline_sha256,
            listing_version=listing_version,
            tools=tools,
            checks=checks,
            overrides=overrides,
            baseline_deployment_id=baseline_deployment_id,
            probe_secret=probe_secret,
        )

        mcp_contract_response.additional_properties = d
        return mcp_contract_response

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
