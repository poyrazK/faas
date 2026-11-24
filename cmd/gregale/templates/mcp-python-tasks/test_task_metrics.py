"""All task health gauges are published, including zero-valued states."""
import json
import unittest
from unittest.mock import MagicMock, Mock, patch
from urllib.error import HTTPError

from task_metrics import _publish_loop


class OneCycle:
    done = False

    def is_set(self):
        return self.done

    def wait(self, _timeout):
        self.done = True


class TaskMetricsPublisherTests(unittest.TestCase):
    def test_observer_publishes_complete_queue_health_before_heartbeat(self):
        runtime = Mock()
        runtime.queue_metrics.return_value = {
            "outstanding_tasks": 5, "oldest_age_seconds": 10, "without_compatible_worker_tasks": 1,
            "running_tasks": 1, "failed_tasks": 2, "capacity_waiting_tasks": 1,
            "retry_waiting_tasks": 0, "active_workers": 1, "draining_workers": 0,
            "unsupported_handler_tasks": 1, "handler_compatibility": [], "compatibility_truncated": False,
        }
        opener = Mock()
        response = MagicMock()
        response.__enter__.return_value.status = 204
        opener.open.return_value = response
        settings = {"api_url": "https://api.example", "app_slug": "test", "token": "test-only"}
        with patch("task_metrics.build_opener", return_value=opener):
            _publish_loop(runtime, settings, True, OneCycle())
        posted = {}
        for call in opener.open.call_args_list:
            request = call.args[0]
            if "/custom-metrics/" in request.full_url:
                posted[request.full_url.rsplit("/", 1)[1]] = json.loads(request.data)["value"]
        expected = {"mcp_tasks_" + name: value for name, value in {
            "outstanding": 5, "oldest_age_seconds": 10, "without_compatible_worker": 1,
            "running": 1, "failed": 2, "capacity_waiting": 1, "retry_waiting": 0,
            "active_workers": 1, "draining_workers": 0, "unsupported_handler_tasks": 1,
            "observer_heartbeat": 1,
        }.items()}
        self.assertEqual(posted, expected)
        self.assertEqual(opener.open.call_count, 12)
        self.assertTrue(opener.open.call_args.args[0].full_url.endswith("/mcp_tasks_observer_heartbeat"))

    def test_publication_has_one_http_deadline_for_the_whole_batch(self):
        runtime = Mock()
        runtime.queue_metrics.return_value = {field: 0 for field in (
            "outstanding_tasks", "oldest_age_seconds", "without_compatible_worker_tasks", "running_tasks",
            "failed_tasks", "capacity_waiting_tasks", "retry_waiting_tasks", "active_workers",
            "draining_workers", "unsupported_handler_tasks",
        )}
        opener = Mock()
        response = MagicMock()
        response.__enter__.return_value.status = 204
        opener.open.return_value = response
        settings = {"api_url": "https://api.example", "app_slug": "test", "token": "test-only"}
        with patch("task_metrics.build_opener", return_value=opener), patch("task_metrics.time.monotonic", side_effect=[0, 1, 3, 6]), patch("task_metrics.logging.error") as error:
            _publish_loop(runtime, settings, False, OneCycle())
        self.assertEqual([call.kwargs["timeout"] for call in opener.open.call_args_list], [4, 2])
        error.assert_called_once()
        self.assertEqual(json.loads(error.call_args.args[0]), {"event": "mcp_task_metrics_publish_failed", "step": "metric:mcp_tasks_without_compatible_worker"})

    def test_failure_log_names_step_and_status_without_secrets(self):
        metrics = {field: 0 for field in (
            "outstanding_tasks", "oldest_age_seconds", "without_compatible_worker_tasks", "running_tasks",
            "failed_tasks", "capacity_waiting_tasks", "retry_waiting_tasks", "active_workers",
            "draining_workers", "unsupported_handler_tasks",
        )}
        metrics.update(handler_compatibility=[], compatibility_truncated=False)
        settings = {"api_url": "https://api.example", "app_slug": "test", "token": "secret-token"}
        ok = MagicMock()
        ok.__enter__.return_value.status = 204

        def refuse_running(request, timeout):
            if request.full_url.endswith("/mcp_tasks_running"):
                raise HTTPError(request.full_url, 403, "Forbidden secret-token", {}, None)
            return ok

        def refuse_report(request, timeout):
            if request.full_url.endswith("/mcp-task-compatibility"):
                raise OSError("connect https://api.example secret-token")
            return ok

        cases = [
            ("gauge refused", False, refuse_running, None, {"event": "mcp_task_metrics_publish_failed", "step": "metric:mcp_tasks_running", "status": 403}),
            ("report unreachable", True, refuse_report, None, {"event": "mcp_task_observer_health_check_failed", "step": "compatibility_report"}),
            ("queue query", False, refuse_running, RuntimeError("postgres://u:secret@h"), {"event": "mcp_task_metrics_publish_failed", "step": "queue_metrics"}),
        ]
        for name, observer, open_fn, query_error, want in cases:
            with self.subTest(name):
                runtime = Mock()
                runtime.queue_metrics.return_value = metrics
                runtime.queue_metrics.side_effect = query_error
                opener = Mock()
                opener.open.side_effect = open_fn
                with patch("task_metrics.build_opener", return_value=opener), patch("task_metrics.logging.error") as error:
                    _publish_loop(runtime, settings, observer, OneCycle())
                logged = error.call_args.args[0]
                self.assertEqual(json.loads(logged), want)
                self.assertNotIn("secret", logged)


class WorkerReceiptTests(unittest.TestCase):
    def test_receipt_names_registry_id_and_drain_budget(self):
        """The CLI release gate reads this receipt (ADR-20261123160000)."""
        import uuid
        from types import SimpleNamespace
        from tasks import DurableTaskRuntime
        worker = SimpleNamespace(_worker_id=uuid.UUID("11111111-1111-4111-8111-111111111111"), drain_timeout=45)
        self.assertEqual(json.loads(DurableTaskRuntime.worker_receipt(worker)), {
            "event": "mcp_task_worker_started", "workerID": "11111111-1111-4111-8111-111111111111",
            "claimFence": False, "shutdownTimeoutMs": 45000})
