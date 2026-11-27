"""Proven lease budgets survive connection failures and stalled renewals."""
import threading
import unittest
from unittest.mock import Mock, patch

from task_execution import TaskLeaseDeadline
from tasks import DurableTaskRuntime


class ManualTimer:
    def __init__(self, interval, callback):
        self.interval, self.callback = interval, callback
        self.cancelled = False

    def start(self):
        pass

    def cancel(self):
        self.cancelled = True


class TaskLeaseDeadlineTests(unittest.TestCase):
    def setUp(self):
        self.now = 0
        self.timers = []
        self.expired = Mock()

    def timer(self, interval, callback):
        timer = ManualTimer(interval, callback)
        self.timers.append(timer)
        return timer

    def lease(self, callback=None):
        lease = TaskLeaseDeadline(callback or self.expired, clock=lambda: self.now, timer_factory=self.timer)
        self.addCleanup(lease.close)
        return lease

    def test_renewal_excludes_response_latency_and_stale_callbacks(self):
        lease = self.lease()
        original = self.timers[-1]
        self.now = 20
        lease.renew(5)
        self.assertEqual(self.timers[-1].interval, 15)
        original.callback()  # A timer already dispatching when renewal completed.
        self.expired.assert_not_called()
        self.now = 35
        self.timers[-1].callback()
        self.expired.assert_called_once_with()
        lease.renew(35)
        self.assertEqual(len(self.timers), 3)

    def test_late_success_cannot_revive_a_deadline_before_timer_dispatch(self):
        lease = self.lease()
        self.now = 31
        lease.renew(31)
        self.expired.assert_called_once_with()
        self.assertTrue(self.timers[-1].cancelled)

    def test_close_removes_finished_execution_deadline(self):
        lease = self.lease()
        lease.close()
        self.now = 60
        self.timers[-1].callback()
        self.expired.assert_not_called()

    def test_runtime_cancels_handler_and_does_not_write_after_outage(self):
        runtime = DurableTaskRuntime.__new__(DurableTaskRuntime)
        runtime._drain_expired = threading.Event()
        runtime._finish = Mock()
        runtime._heartbeat = Mock(side_effect=RuntimeError("database unavailable"))
        started = threading.Event()

        def execute(_args, context):
            started.set()
            context.cancelled.wait(2)
            return {"content": []}

        runtime._task_executors = {("build_report", "1"): execute}
        task = {"task_id": "test-task", "lease_token": "test-lease", "tool_name": "build_report", "handler_version": "1", "arguments": {}}
        cancelled = threading.Event()
        with patch("tasks.TaskLeaseDeadline", side_effect=lambda callback, _started: self.lease(callback)):
            worker = threading.Thread(target=runtime._run_task, args=(task, cancelled))
            worker.start()
            self.assertTrue(started.wait(1))
            self.now = 30
            self.timers[-1].callback()
            worker.join(timeout=1)
        self.assertFalse(worker.is_alive())
        self.assertTrue(cancelled.is_set())
        runtime._finish.assert_not_called()
