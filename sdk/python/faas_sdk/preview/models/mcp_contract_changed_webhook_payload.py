from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_contract_changed_webhook_payload_verdict import (
    MCPContractChangedWebhookPayloadVerdict,
    check_mcp_contract_changed_webhook_payload_verdict,
)

T = TypeVar("T", bound="MCPContractChangedWebhookPayload")


@_attrs_define
class MCPContractChangedWebhookPayload:
    """Data delivered with mcp.contract.changed when a candidate that changed a listed MCP server's tool contract went
    live.

    """

    app_id: UUID
    deployment_id: UUID
    verdict: MCPContractChangedWebhookPayloadVerdict
    baseline_sha256: str
    contract_sha256: None | str
    """Null when the catalog could not be listed."""
    overridden: bool
    tools: list[str]
    changes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        verdict: str = self.verdict

        baseline_sha256 = self.baseline_sha256

        contract_sha256: None | str
        contract_sha256 = self.contract_sha256

        overridden = self.overridden

        tools = self.tools

        changes = self.changes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "verdict": verdict,
                "baseline_sha256": baseline_sha256,
                "contract_sha256": contract_sha256,
                "overridden": overridden,
                "tools": tools,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        verdict = check_mcp_contract_changed_webhook_payload_verdict(d.pop("verdict"))

        baseline_sha256 = d.pop("baseline_sha256")

        def _parse_contract_sha256(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        contract_sha256 = _parse_contract_sha256(d.pop("contract_sha256"))

        overridden = d.pop("overridden")

        tools = cast(list[str], d.pop("tools"))

        changes = d.pop("changes")

        mcp_contract_changed_webhook_payload = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            verdict=verdict,
            baseline_sha256=baseline_sha256,
            contract_sha256=contract_sha256,
            overridden=overridden,
            tools=tools,
            changes=changes,
        )

        mcp_contract_changed_webhook_payload.additional_properties = d
        return mcp_contract_changed_webhook_payload

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
