"""Mount SDK application adapters in the starter's existing OAuth catalog."""

import asyncio
import json
import threading


class ToolkitResourceNotFound(Exception):
    """A published template did not resolve to a resource this caller can read."""


def _merge_scopes(existing, added):
    if existing is None:
        existing = {}
    if not isinstance(existing, dict) or existing.keys() & added.keys():
        raise ValueError("Invalid or duplicate toolkit scope policy")
    return {**existing, **added}


def _access_for(context, resolve_principal):
    from faas_sdk.preview import ToolkitAccess

    if not context["subject"]:
        return ToolkitAccess(None, ())
    scopes = tuple(context["scopes"])
    principal = (
        resolve_principal({**context, "scopes": scopes})
        if resolve_principal
        else context["subject"]
    )
    return ToolkitAccess(principal, scopes)


class TaskSubscriptionBudget:
    """Bound concurrent Task watches for queued and pipeline subscriptions."""

    def __init__(self):
        self._subscription_lock = threading.Lock()
        self._subscription_count = 0
        self._watched_task_count = 0

    def reserve_task_subscription(self):
        with self._subscription_lock:
            if self._subscription_count >= 64:
                return False
            self._subscription_count += 1
            return True

    def reserve_watched_task_ids(self, count):
        with self._subscription_lock:
            if count < 0 or self._watched_task_count + count > 512:
                return False
            self._watched_task_count += count
            return True

    def release_task_subscription(self, task_count):
        with self._subscription_lock:
            task_count = min(max(task_count, 0), self._watched_task_count)
            self._watched_task_count -= task_count
            if self._subscription_count > 0:
                self._subscription_count -= 1

    def release_watched_task_ids(self, count):
        with self._subscription_lock:
            count = min(max(count, 0), self._watched_task_count)
            self._watched_task_count -= count


class ToolkitTaskReadFailed(Exception):
    """A configured pipeline Task could not be read."""


def mount_pipeline_tasks(config, pipeline_tasks, native=None, resolve_principal=None):
    """Use the starter's Tasks protocol for the SDK's owned pipeline bindings."""
    from faas_sdk.preview import PipelineTaskCaller, QueryToolError
    from tasks import TaskNotFound, _input_methods_for

    if config.get("auth", {}).get("mode") != "external-oauth":
        raise ValueError("Pipeline Tasks require external-oauth")
    if any(not callable(getattr(pipeline_tasks, name, None)) for name in ("can_create", "create", "get")):
        raise ValueError("Invalid pipeline Task runtime")

    class TaskRuntime:
        def __getattr__(self, name):
            return getattr(native, name)

        def _caller(self, identity, capabilities=None):
            subject, client_id = getattr(identity, "subject", ""), getattr(identity, "client_id", "")
            access = _access_for({"subject": subject, "client_id": client_id, "scopes": sorted(identity)}, resolve_principal)
            return PipelineTaskCaller(
                config["auth"]["resource"], subject, client_id, access,
                supports_forms="elicitation/create:form" in _input_methods_for(capabilities),
            )

        def can_create(self, name):
            return pipeline_tasks.can_create(name) or (native is not None and native.can_create(name))

        def can_submit_keyed(self, name):
            return native is not None and native.can_create(name) and not pipeline_tasks.can_create(name)

        def create(self, name, args, identity, capabilities, output_schema=None, submission_key=None):
            if not pipeline_tasks.can_create(name):
                if native is None:
                    raise ValueError("Task creation unavailable")
                return native.create(name, args, identity, capabilities, output_schema=output_schema, submission_key=submission_key)
            try:
                return asyncio.run(pipeline_tasks.create(name, args, self._caller(identity), timeout=30))
            except QueryToolError as error:
                if error.code == "invalid_input":
                    raise ValueError("Invalid pipeline Task arguments") from None
                raise

        def get(self, task_id, identity, authenticator, scopes):
            return self.get_with_client_capabilities(task_id, identity, authenticator, scopes, None)

        def get_with_client_capabilities(self, task_id, identity, authenticator, scopes, capabilities):
            try:
                return self.get_pipeline_task(task_id, identity, capabilities), None
            except TaskNotFound:
                if native is None:
                    raise
                return native.get(task_id, identity, authenticator, scopes)

        def get_pipeline_task(self, task_id, identity, capabilities, *, timeout=30):
            try:
                return asyncio.run(pipeline_tasks.get(task_id, self._caller(identity, capabilities), timeout=timeout))
            except QueryToolError as error:
                if error.code == "not_found":
                    raise TaskNotFound from None
                raise ToolkitTaskReadFailed from None
            except Exception:
                raise ToolkitTaskReadFailed from None

        def cancel(self, task_id, identity, authenticator, scopes):
            if native is None:
                raise TaskNotFound
            return native.cancel(task_id, identity, authenticator, scopes)

        def update_inputs(self, task_id, responses, identity, authenticator, scopes):
            if callable(getattr(pipeline_tasks, "update", None)):
                try:
                    asyncio.run(pipeline_tasks.update(task_id, responses, self._caller(identity), timeout=30))
                    return
                except QueryToolError as error:
                    if error.code == "invalid_input":
                        raise ValueError("Invalid or stale pipeline Task response") from None
                    if error.code != "not_found":
                        raise
            if native is None:
                raise TaskNotFound
            return native.update_inputs(task_id, responses, identity, authenticator, scopes)

    return TaskRuntime()


def install_application_toolkit(config, catalog, toolkit, resolve_principal=None, *, pipeline_tasks=None):
    """Register before serving and return the configuration used by Authenticator."""
    if config.get("auth", {}).get("mode") != "external-oauth":
        raise ValueError("Application toolkits require external-oauth")
    if resolve_principal is not None and not callable(resolve_principal):
        raise TypeError("Expected a toolkit principal resolver")
    auth = config["auth"]
    manifest_uri = getattr(pipeline_tasks, "manifest_resource_uri", None)
    recovery = getattr(pipeline_tasks, "recovery_tools", None)
    handoffs = getattr(pipeline_tasks, "handoff_tools", None)
    handoff_operations = [handoffs[key] for key in ("read", "reply", "list", "approval", "decide") if key in handoffs] if handoffs else []
    handoff_scopes = {definition["name"]: handoffs["scopes"] for definition in handoff_operations}
    recovery_scopes = {
        recovery["preview"]["name"]: recovery["scopes"],
        recovery["confirm"]["name"]: recovery["scopes"],
    } if recovery else {}
    resource_scopes = _merge_scopes(toolkit.resource_scopes, {manifest_uri: auth.get("scopes", [])} if manifest_uri else {})
    configured = {
        **config,
        "auth": {
            **auth,
            "tool_scopes": _merge_scopes(auth.get("tool_scopes"), _merge_scopes(toolkit.tool_scopes, _merge_scopes(recovery_scopes, handoff_scopes))),
            "resource_scopes": _merge_scopes(
                auth.get("resource_scopes"), resource_scopes
            ),
        },
    }


    def tool_handler(name):
        def dispatch(args, context):
            try:
                return asyncio.run(
                    toolkit.call(name, args, _access_for(context, resolve_principal), timeout=30)
                )
            except Exception as error:  # noqa: BLE001 - redact application exceptions
                code = getattr(error, "code", None)
                known = {"tool_not_found", "limit"}
                code = (
                    code
                    if isinstance(code, str) and code in known
                    else "invalid_input"
                    if code == -32602
                    else "request_failed"
                )
                return {
                    "isError": True,
                    "content": [
                        {
                            "type": "text",
                            "text": json.dumps({"code": code}, separators=(",", ":")),
                        }
                    ],
                }

        return dispatch

    def read(uri, _variables, context):
        try:
            return asyncio.run(
                toolkit.read_resource(uri, _access_for(context, resolve_principal), timeout=30)
            )["contents"]
        except Exception as error:
            if getattr(error, "code", None) in ("resource_not_found", -32002):
                raise ToolkitResourceNotFound from None
            raise

    if handoffs:
        from faas_sdk.preview import PipelineTaskCaller

        def handoff_handler(name):
            def dispatch(args, context):
                caller = PipelineTaskCaller(
                    auth["resource"], context["subject"], context["client_id"], _access_for(context, resolve_principal),
                )
                try:
                    return asyncio.run(pipeline_tasks.call_handoff(name, args, caller, timeout=30))
                except Exception as error:
                    code = getattr(error, "code", None)
                    code = code if code in ("not_found", "invalid_input", "deadline") else "request_failed"
                    return {"isError": True, "content": [{"type": "text", "text": json.dumps({"code": code})}]}

            return dispatch

        for definition in handoff_operations:
            catalog.register("tool", definition, handoff_handler(definition["name"]))
    if recovery:
        from faas_sdk.preview import PipelineTaskCaller

        def preview_recovery(args, context):
            caller = PipelineTaskCaller(
                auth["resource"], context["subject"], context["client_id"], _access_for(context, resolve_principal),
            )
            try:
                return asyncio.run(pipeline_tasks.preview_recovery(args, caller, timeout=30))
            except Exception as error:
                code = getattr(error, "code", None)
                code = code if code in ("invalid_input", "not_found") else "request_failed"
                return {"isError": True, "content": [{"type": "text", "text": json.dumps({"code": code})}]}

        def confirm_recovery_requires_tasks(_args, _context):
            return {"isError": True, "content": [{"type": "text", "text": '{"code":"task_mode_required"}'}]}

        catalog.register("tool", recovery["preview"], preview_recovery)
        catalog.register("tool", recovery["confirm"], confirm_recovery_requires_tasks)
    for definition in toolkit.tools:
        catalog.register("tool", definition, tool_handler(definition["name"]))
    for template in toolkit.resource_templates:
        catalog.register("resource", template, read)
    if manifest_uri:
        def read_manifest(_uri, _variables, context):
            return pipeline_tasks.read_manifest(_access_for(context, resolve_principal))["contents"]

        catalog.register("resource", {
            "uri": manifest_uri, "name": "pipeline_task_manifest", "mimeType": "application/json",
            "description": "Discover authorized pipeline Task contracts.",
        }, read_manifest)
    return configured
