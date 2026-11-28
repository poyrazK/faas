from typing import Literal

MCPContractChangedWebhookPayloadVerdict = Literal["breaking", "compatible", "review", "unverified"]

MCP_CONTRACT_CHANGED_WEBHOOK_PAYLOAD_VERDICT_VALUES: set[MCPContractChangedWebhookPayloadVerdict] = {
    "breaking",
    "compatible",
    "review",
    "unverified",
}


def check_mcp_contract_changed_webhook_payload_verdict(value: str) -> MCPContractChangedWebhookPayloadVerdict:
    if value in MCP_CONTRACT_CHANGED_WEBHOOK_PAYLOAD_VERDICT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MCP_CONTRACT_CHANGED_WEBHOOK_PAYLOAD_VERDICT_VALUES!r}"
    )
