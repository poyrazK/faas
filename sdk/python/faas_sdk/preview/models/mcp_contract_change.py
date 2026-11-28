from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_contract_change_severity import MCPContractChangeSeverity, check_mcp_contract_change_severity
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPContractChange")


@_attrs_define
class MCPContractChange:
    """One difference between a candidate's MCP catalog and the promised contract."""

    path: str
    """JSON pointer into the contract."""
    kind: str
    severity: MCPContractChangeSeverity
    tool: str | Unset = UNSET
    resource: str | Unset = UNSET
    resource_template: str | Unset = UNSET
    prompt: str | Unset = UNSET
    capability: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        kind = self.kind

        severity: str = self.severity

        tool = self.tool

        resource = self.resource

        resource_template = self.resource_template

        prompt = self.prompt

        capability = self.capability

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "path": path,
                "kind": kind,
                "severity": severity,
            }
        )
        if tool is not UNSET:
            field_dict["tool"] = tool
        if resource is not UNSET:
            field_dict["resource"] = resource
        if resource_template is not UNSET:
            field_dict["resource_template"] = resource_template
        if prompt is not UNSET:
            field_dict["prompt"] = prompt
        if capability is not UNSET:
            field_dict["capability"] = capability

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        kind = d.pop("kind")

        severity = check_mcp_contract_change_severity(d.pop("severity"))

        tool = d.pop("tool", UNSET)

        resource = d.pop("resource", UNSET)

        resource_template = d.pop("resource_template", UNSET)

        prompt = d.pop("prompt", UNSET)

        capability = d.pop("capability", UNSET)

        mcp_contract_change = cls(
            path=path,
            kind=kind,
            severity=severity,
            tool=tool,
            resource=resource,
            resource_template=resource_template,
            prompt=prompt,
            capability=capability,
        )

        mcp_contract_change.additional_properties = d
        return mcp_contract_change

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
