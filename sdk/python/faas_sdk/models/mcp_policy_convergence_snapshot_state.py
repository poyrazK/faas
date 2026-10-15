from typing import Literal

MCPPolicyConvergenceSnapshotState = Literal["active", "paused", "pending", "unknown", "unverified"]

MCP_POLICY_CONVERGENCE_SNAPSHOT_STATE_VALUES: set[MCPPolicyConvergenceSnapshotState] = {
    "active",
    "paused",
    "pending",
    "unknown",
    "unverified",
}


def check_mcp_policy_convergence_snapshot_state(value: str) -> MCPPolicyConvergenceSnapshotState:
    if value in MCP_POLICY_CONVERGENCE_SNAPSHOT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MCP_POLICY_CONVERGENCE_SNAPSHOT_STATE_VALUES!r}")
