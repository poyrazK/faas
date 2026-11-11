"""Default form mode remains valid through durable persistence and replay."""
import copy
import unittest
from unittest.mock import MagicMock, Mock

from tasks import DurableTaskRuntime, _input_methods_for


class TaskInputCapabilityTests(unittest.TestCase):
    def test_empty_capability_objects_are_supported(self):
        self.assertEqual(_input_methods_for({"elicitation": {"form": {}, "url": {}}}),
                         ["elicitation/create:form", "elicitation/create:url"])
        self.assertEqual(_input_methods_for({"elicitation": {}}), ["elicitation/create:form"])


class TaskInputTests(unittest.TestCase):
    def setUp(self):
        self.runtime = DurableTaskRuntime.__new__(DurableTaskRuntime)
        self.runtime.namespace = "test"
        self.runtime.pool = MagicMock()
        self.runtime._encrypt = Mock(return_value=b"encrypted-state")
        self.connection = self.runtime.pool.connection.return_value.__enter__.return_value
        self.connection.execute.return_value.fetchone.return_value = {
            "input_state_encrypted": None, "cancel_requested_at": None,
        }
        self.connection.execute.return_value.rowcount = 1
        self.task = {"task_id": "task-id", "lease_token": "lease-token", "input_methods": ["elicitation/create:form"]}
        self.requests = {"approval": {"method": "elicitation/create", "params": {
            "message": "Approve?", "requestedSchema": {"type": "object", "properties": {}},
        }}}

    def test_default_form_parks_and_replays_without_changing_request(self):
        original = copy.deepcopy(self.requests)
        self.assertEqual(self.runtime._request_inputs(self.task, self.requests), ({}, False))
        self.assertEqual(self.requests, original)
        state = self.runtime._encrypt.call_args.args[0]
        self.assertEqual(state["requests"], original)
        self.assertTrue(any("status='input_required'" in call.args[0] for call in self.connection.execute.call_args_list))
        response = {"action": "accept", "content": {}}
        state["responses"]["approval"] = response
        self.runtime._decrypt = Mock(return_value=state)
        self.connection.execute.return_value.fetchone.return_value["input_state_encrypted"] = b"encrypted-state"
        self.assertEqual(self.runtime._request_inputs(self.task, self.requests), ({"approval": response}, True))
        self.assertEqual(self.runtime._encrypt.call_count, 1)

    def test_explicit_invalid_modes_fail_before_accessing_storage(self):
        for mode in (None, "", "invalid", 1):
            with self.subTest(mode=mode):
                self.requests["approval"]["params"]["mode"] = mode
                with self.assertRaises(ValueError):
                    self.runtime._request_inputs(self.task, self.requests)
        self.runtime.pool.connection.assert_not_called()

    def test_default_form_still_requires_client_capability(self):
        self.task["input_methods"] = ["elicitation/create:url"]
        with self.assertRaisesRegex(ValueError, "client did not declare support"):
            self.runtime._request_inputs(self.task, self.requests)
        self.runtime.pool.connection.assert_not_called()
