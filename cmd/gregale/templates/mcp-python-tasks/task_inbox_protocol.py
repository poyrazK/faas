"""Wire values and exact JSON for the Task inbox and reviewed input."""

from decimal import Decimal
import json
import re

TASK_INBOX_EXTENSION = "io.gregale/task-inbox"
TASK_INPUT_REVIEW_EXTENSION = "io.gregale/task-input-review"
TASK_INBOX_METHOD = "gregale/tasks/inbox"
TASK_INPUT_REVIEW_METHOD = "gregale/tasks/input-review"
TASK_RESPOND_REVIEWED_METHOD = "gregale/tasks/respond-reviewed"
TASK_CHECK_REVIEWED_METHOD = "gregale/tasks/check-reviewed-response"
TASK_REVIEW_RESPONSE_METHODS = (TASK_RESPOND_REVIEWED_METHOD, TASK_CHECK_REVIEWED_METHOD)
TASK_INBOX_METHODS = (TASK_INBOX_METHOD, TASK_INPUT_REVIEW_METHOD, *TASK_REVIEW_RESPONSE_METHODS)

INBOX_STATUSES = ("working", "input_required", "completed", "failed", "cancelled")
INBOX_CURSOR = re.compile(r"[A-Za-z0-9_-]{1,128}\.[A-Za-z0-9_-]{43}")
REVIEW_REVISION = re.compile(r"[0-9a-f]{64}")
MAX_REVIEWED_FORMS = 16


class InvalidTaskInboxRequest(ValueError):
    pass


def _reject_constant(_value):
    raise ValueError("invalid JSON number")


def load_review_json(value):
    return json.loads(value, parse_int=Decimal, parse_float=Decimal, parse_constant=_reject_constant)


def dump_review_json(value, *, sort_keys=False):
    """Emit exact decimal values as JSON numbers without converting to float."""
    def encode(item):
        if isinstance(item, Decimal):
            if not item.is_finite():
                raise ValueError("invalid JSON number")
            return str(item)
        if isinstance(item, dict):
            keys = sorted(item) if sort_keys else item
            return "{" + ",".join(json.dumps(key) + ":" + encode(item[key]) for key in keys) + "}"
        if isinstance(item, list):
            return "[" + ",".join(encode(entry) for entry in item) + "]"
        return json.dumps(item, separators=(",", ":"), allow_nan=False)

    return encode(value)
