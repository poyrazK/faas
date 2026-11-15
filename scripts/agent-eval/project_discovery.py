"""Grade discovery using returned MCP evidence, not an agent's success claim."""
import hashlib
import json


def fingerprints(project):
    result = {}
    for path in project.rglob("*"):
        if ".git" in path.relative_to(project).parts:
            continue
        if path.is_symlink():
            result[str(path.relative_to(project))] = "symlink:" + str(path.readlink())
        elif path.is_file():
            result[str(path.relative_to(project))] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def _payload(result):
    if isinstance(result, str):
        try:
            return _payload(json.loads(result))
        except ValueError:
            return None
    if isinstance(result, list):
        for block in result:
            value = _payload(block)
            if value:
                return value
    if not isinstance(result, dict) or result.get("isError") or result.get("is_error"):
        return None
    if result.get("schema_version") == 1:
        return result
    for key in ("structuredContent", "structured_content", "content", "text"):
        if key in result:
            value = _payload(result[key])
            if value:
                return value
    return None


def discovery_checks(lines, metrics, project, before, created):
    calls, results = {}, []
    for line in lines:
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if not isinstance(event, dict):
            continue
        # Claude Code tool_use/tool_result pairs.
        message = event.get("message")
        blocks = message.get("content") if isinstance(message, dict) else []
        for block in blocks or []:
            if not isinstance(block, dict):
                continue
            if event.get("type") == "assistant" and block.get("type") == "tool_use":
                calls[block.get("id")] = block.get("name", "")
            elif event.get("type") == "user" and block.get("type") == "tool_result" and not block.get("is_error"):
                results.append((calls.get(block.get("tool_use_id"), ""), _payload(block.get("content"))))
        # Codex records the returned MCP result on a completed tool item.
        item = event.get("item") or {}
        if not isinstance(item, dict):
            continue
        if event.get("type") == "item.completed" and item.get("type") == "mcp_tool_call" and item.get("status") == "completed" and not item.get("error"):
            results.append(("mcp__" + item.get("server", "") + "__" + item.get("tool", ""), _payload(item.get("result"))))
    assessment = workflow = False
    for name, value in results:
        if not value or not name.startswith("mcp__gregale__"):
            continue
        if name.endswith("__gregale_assess_project"):
            names = {w.get("name") for w in value.get("workloads") or [] if isinstance(w, dict)}
            kinds = {d.get("kind") for d in value.get("dependencies") or [] if isinstance(d, dict)}
            features = {f.get("id") for f in value.get("features") or [] if isinstance(f, dict)}
            assessment |= names == {"web", "api"} and kinds == {"postgres"} and {"managed-postgres", "private-services", "project-releases"} <= features
        if name.endswith("__gregale_get_workflow"):
            workflow |= value.get("task") == "deploy_project" and any(isinstance(s, dict) and s.get("reason") == "project_deploy_requires_cli" and s.get("status") == "unsupported" for s in value.get("steps") or [])
    prohibited = any(any(word in name for word in ("gregale_apply_", "gregale_promote_", "gregale_start_", "gregale_cancel_", "gregale_request_secret", "gregale_create_")) for name in metrics.get("tool_calls", {}))
    return [
        {"kind": "discovery_evidence", "ok": assessment and workflow, "detail": "requires successful assessment and project workflow MCP results (Claude Code/Codex transcript formats)"},
        {"kind": "discovery_read_only", "ok": not prohibited and not metrics.get("commands") and not metrics.get("written_paths") and fingerprints(project) == before and not any(created.values()), "detail": "requires no shell, write tools, file changes or created resources"},
    ]
