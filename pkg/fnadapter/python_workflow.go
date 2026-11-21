package fnadapter

// PythonWorkflow serves a workflow function (ADR-20261113120824 §1): an
// engine call (X-Faas-Workflow-Mode: code) runs handler(input, wf) under the
// protocol core of the faas_sdk in the function's dependencies.
const PythonWorkflow = `
def _workflow_headers(request):
    out = {}
    raw = request.get("headers") if isinstance(request, dict) else None
    for key, value in (raw or {}).items():
        if isinstance(value, list):
            value = value[0] if value else ""
        out[str(key).lower()] = value
    return out

def _is_workflow_call(request):
    return _workflow_headers(request).get("x-faas-workflow-mode") == "code"

# A workflow function (FAAS_FUNCTION_WORKFLOW=1, stamped by imaged) runs
# only through workflow runs; any other request gets a 404 problem instead of
# handler(event, ctx), which would reach wf.call on the ctx and answer 500.
_IS_WORKFLOW_FUNCTION = os.environ.get("FAAS_FUNCTION_WORKFLOW", "") == "1"

def _workflow_plain_call_problem(request):
    if not _IS_WORKFLOW_FUNCTION or _is_workflow_call(request):
        return None
    return _workflow_problem(404, "workflow_function_not_invocable",
                             "this function is a workflow: start a run with functions.<name>.start() or gregale workflow run")

def _workflow_problem(status, code, detail):
    docs = "https://gregale.dev/docs/errors#" + code
    return _whole(status, {"content-type": "application/problem+json"},
                  {"type": docs, "title": detail, "status": status, "code": code, "detail": detail})

async def _workflow_envelope(request):
    try:
        from faas_sdk import workflows as _workflows
    except ImportError:
        return _workflow_problem(500, "workflow_sdk_missing", "a workflow function needs faas_sdk in its dependencies")
    if not _workflow_headers(request).get("x-faas-workflow-run-id"):
        return _whole(403, {"content-type": "application/json"}, {"error": "workflow entry handlers only accept Gregale workflow calls"})
    try:
        body = json.loads(base64.b64decode(request.get("body_b64") or "") or b"null")
    except ValueError:
        return _whole(400, {"content-type": "application/json"}, {"error": "workflow request is not JSON"})
    name = body.get("workflow", "") if isinstance(body, dict) else ""

    async def run(workflow_input, wf):
        result = handler(workflow_input, wf)
        if inspect.isawaitable(result):
            result = await result
        return result

    try:
        response = await _workflows.run_workflow_invocation(_workflows.WorkflowDefinition(name=name, run=run), body)
    except _workflows.UnknownWorkflowError as exc:
        return _whole(404, {"content-type": "application/json"}, {"error": str(exc)})
    except ValueError as exc:
        return _whole(400, {"content-type": "application/json"}, {"error": str(exc)})
    return _whole(200, {"content-type": "application/json"}, response)
`
