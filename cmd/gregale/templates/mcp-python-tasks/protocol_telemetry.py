"""Bounded protocol observations; never serialize request or result data."""
import json
import re
import time

METHODS = frozenset(("initialize", "server/discover", "ping", "tools/list", "tools/call", "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "completion/complete", "tasks/get", "tasks/cancel", "tasks/update", "subscriptions/listen"))
TOOLS = frozenset(("greet", "add", "stream_demo", "build_report"))
REQUEST_ID = re.compile(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
TRACEPARENT = re.compile(r"00-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}")


class ProtocolObservation:
    def __init__(self, message, headers, sink=None):
        method = message.get("method")
        self.method = method if isinstance(method, str) and method in METHODS else "other"
        params = message.get("params")
        name = params.get("name") if isinstance(params, dict) else None
        self.tool = (name if isinstance(name, str) and name in TOOLS else "other") if method == "tools/call" else "none"
        self.start = time.monotonic()
        self.execution_end = self.stream_start = None
        self.outcome = None
        self.stream_outcome = "complete"
        self.sink = sink or (lambda event: print(json.dumps(event, separators=(",", ":")), flush=True))
        ids = headers.get_all("X-Faas-Request-Id", [])
        self.request_id = ids[0] if len(ids) == 1 and REQUEST_ID.fullmatch(ids[0]) else None
        traces = headers.get_all("traceparent", [])
        trace = TRACEPARENT.fullmatch(traces[0]) if len(traces) == 1 else None
        self.trace_id = trace[1] if trace and trace[1] != "0" * 32 and trace[2] != "0" * 16 else None

    def response(self, value):
        if not isinstance(value, dict):
            return
        if value.get("method") == "notifications/subscriptions/acknowledged":
            if self.execution_end is None:self.execution_end = time.monotonic()
            return
        if "result" not in value and "error" not in value or self.outcome is not None:
            return
        if self.execution_end is None:self.execution_end = time.monotonic()
        result = value.get("result")
        self.outcome = "rpc_error" if "error" in value else "tool_error" if isinstance(result, dict) and result.get("isError") is True else "task_created" if isinstance(result, dict) and result.get("resultType") == "task" else "success"

    def failed(self, reason):
        self.stream_outcome = reason
        if self.outcome is None:self.outcome = reason

    def finish(self):
        now = time.monotonic()
        event = {"event": "mcp_protocol_request", "method": self.method, "tool": self.tool, "outcome": self.outcome or "unobserved", "stream_outcome": self.stream_outcome}
        if self.execution_end is not None:event["execution_duration_ms"] = max(0, (self.execution_end - self.start) * 1000)
        if self.stream_start is not None:event["stream_duration_ms"] = max(0, (now - self.stream_start) * 1000)
        if self.request_id:event["request_id"] = self.request_id
        if self.trace_id:event["trace_id"] = self.trace_id
        try:self.sink(event)
        except Exception:pass  # Telemetry cannot fail the application request.


def start_protocol_heartbeat():
    """Return an idempotent stop callback; never hold process exit open."""
    import os
    import threading
    stopped = threading.Event()
    def emit():
        try:
            print(json.dumps({"event": "mcp_telemetry_heartbeat", "version": 1,
                              "loss_alert": os.environ.get("MCP_TELEMETRY_LOSS_ALERT") == "true"}), flush=True)
        except Exception:
            pass
    def run():
        while not stopped.wait(30):
            emit()
    emit()
    threading.Thread(target=run, daemon=True).start()
    return stopped.set
