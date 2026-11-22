"""This starter answers every tool call in its response and ships without
durable MCP Tasks. server.py is shared with the mcp-python-tasks starter, so
this module supplies the Task names it imports, with the runtime always
disabled. To add durable Tasks, start from
`gregale init --template mcp-python-tasks`.
"""

import re

TASK_ID_PATTERN = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$")

NO_TASKS = "this server has no durable MCP Tasks; start from the mcp-python-tasks starter"


class TaskNotFound(Exception):
    pass


def resolve_task_role(role=None):
    if role not in (None, "combined", "web"):
        raise ValueError(NO_TASKS)
    return role or "combined"


def register_task_handler(name, *, version, execute, validate, previous_versions=None):
    raise ValueError(NO_TASKS)


class DurableTaskRuntime:
    """Always disabled: server.py drops a runtime that is not enabled."""

    enabled = False

    def __init__(self, config, role="combined"):
        tasks = config.get("tasks") if isinstance(config, dict) else None
        if tasks not in (None, {}) and not (isinstance(tasks, dict) and tasks.get("enabled") is False):
            raise ValueError(NO_TASKS)

    def close(self):
        pass


def task_resource_template():
    return None


def task_id_from_uri(uri):
    return None
