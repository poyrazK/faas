from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.mcp_resource_policy_prompt_scopes_type_0 import MCPResourcePolicyPromptScopesType0
    from ..models.mcp_resource_policy_resource_scopes_type_0 import MCPResourcePolicyResourceScopesType0
    from ..models.mcp_resource_policy_tool_scopes_type_0 import MCPResourcePolicyToolScopesType0


T = TypeVar("T", bound="MCPResourcePolicy")


@_attrs_define
class MCPResourcePolicy:
    """Opt-in MCP resource-server policy on a JWT edge rule. Requires a
    canonical HTTPS resource, matching JWT audience, match_path=/**,
    and no method or header selectors. The gateway serves OAuth resource
    metadata, rejects expired credentials, and enforces JSON-RPC execution
    scopes. Catalog filtering and task ownership remain application duties.

    """

    resource: str
    scopes: list[str] | Unset = UNSET
    allowed_origins: list[str] | Unset = UNSET
    tool_scopes: MCPResourcePolicyToolScopesType0 | None | Unset = UNSET
    resource_scopes: MCPResourcePolicyResourceScopesType0 | None | Unset = UNSET
    """Absolute URIs or simple variable templates; every matching entry must authorize access."""
    prompt_scopes: MCPResourcePolicyPromptScopesType0 | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.mcp_resource_policy_prompt_scopes_type_0 import MCPResourcePolicyPromptScopesType0
        from ..models.mcp_resource_policy_resource_scopes_type_0 import MCPResourcePolicyResourceScopesType0
        from ..models.mcp_resource_policy_tool_scopes_type_0 import MCPResourcePolicyToolScopesType0

        resource = self.resource

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = self.scopes

        allowed_origins: list[str] | Unset = UNSET
        if not isinstance(self.allowed_origins, Unset):
            allowed_origins = self.allowed_origins

        tool_scopes: dict[str, Any] | None | Unset
        if isinstance(self.tool_scopes, Unset):
            tool_scopes = UNSET
        elif isinstance(self.tool_scopes, MCPResourcePolicyToolScopesType0):
            tool_scopes = self.tool_scopes.to_dict()
        else:
            tool_scopes = self.tool_scopes

        resource_scopes: dict[str, Any] | None | Unset
        if isinstance(self.resource_scopes, Unset):
            resource_scopes = UNSET
        elif isinstance(self.resource_scopes, MCPResourcePolicyResourceScopesType0):
            resource_scopes = self.resource_scopes.to_dict()
        else:
            resource_scopes = self.resource_scopes

        prompt_scopes: dict[str, Any] | None | Unset
        if isinstance(self.prompt_scopes, Unset):
            prompt_scopes = UNSET
        elif isinstance(self.prompt_scopes, MCPResourcePolicyPromptScopesType0):
            prompt_scopes = self.prompt_scopes.to_dict()
        else:
            prompt_scopes = self.prompt_scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "resource": resource,
            }
        )
        if scopes is not UNSET:
            field_dict["scopes"] = scopes
        if allowed_origins is not UNSET:
            field_dict["allowed_origins"] = allowed_origins
        if tool_scopes is not UNSET:
            field_dict["tool_scopes"] = tool_scopes
        if resource_scopes is not UNSET:
            field_dict["resource_scopes"] = resource_scopes
        if prompt_scopes is not UNSET:
            field_dict["prompt_scopes"] = prompt_scopes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_resource_policy_prompt_scopes_type_0 import MCPResourcePolicyPromptScopesType0
        from ..models.mcp_resource_policy_resource_scopes_type_0 import MCPResourcePolicyResourceScopesType0
        from ..models.mcp_resource_policy_tool_scopes_type_0 import MCPResourcePolicyToolScopesType0

        d = dict(src_dict)
        resource = d.pop("resource")

        scopes = cast(list[str], d.pop("scopes", UNSET))

        allowed_origins = cast(list[str], d.pop("allowed_origins", UNSET))

        def _parse_tool_scopes(data: object) -> MCPResourcePolicyToolScopesType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                tool_scopes_type_0 = MCPResourcePolicyToolScopesType0.from_dict(data)

                return tool_scopes_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MCPResourcePolicyToolScopesType0 | None | Unset, data)

        tool_scopes = _parse_tool_scopes(d.pop("tool_scopes", UNSET))

        def _parse_resource_scopes(data: object) -> MCPResourcePolicyResourceScopesType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                resource_scopes_type_0 = MCPResourcePolicyResourceScopesType0.from_dict(data)

                return resource_scopes_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MCPResourcePolicyResourceScopesType0 | None | Unset, data)

        resource_scopes = _parse_resource_scopes(d.pop("resource_scopes", UNSET))

        def _parse_prompt_scopes(data: object) -> MCPResourcePolicyPromptScopesType0 | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                prompt_scopes_type_0 = MCPResourcePolicyPromptScopesType0.from_dict(data)

                return prompt_scopes_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MCPResourcePolicyPromptScopesType0 | None | Unset, data)

        prompt_scopes = _parse_prompt_scopes(d.pop("prompt_scopes", UNSET))

        mcp_resource_policy = cls(
            resource=resource,
            scopes=scopes,
            allowed_origins=allowed_origins,
            tool_scopes=tool_scopes,
            resource_scopes=resource_scopes,
            prompt_scopes=prompt_scopes,
        )

        mcp_resource_policy.additional_properties = d
        return mcp_resource_policy

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
