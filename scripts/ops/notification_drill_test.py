#!/usr/bin/env python3
"""The local drill receiver must verify correlation before acknowledging."""

import copy
from datetime import datetime, timezone
import http.server
import json
import threading
import unittest
import urllib.error
import urllib.request

import notification_drill


class DrillReceiverTest(unittest.TestCase):
    def setUp(self):
        self.receiver = notification_drill.Receiver("test-correlation")
        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), self.receiver.handler())
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()

    def payload(self):
        at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        return {"receiver": "faas-page", "status": "firing", "alerts": [
            {"status": "firing", "annotations": {"drill_id": "test-correlation"}, "labels": {"stage": "baseline"}, "startsAt": at, "endsAt": at}]}

    def post(self, payload):
        req = urllib.request.Request(f"http://127.0.0.1:{self.server.server_port}/receive/faas-page",
                                     data=json.dumps(payload).encode(), headers={"Content-Type": "application/json"})
        try:
            with notification_drill.OPENER.open(req, timeout=2) as response:
                return response.status
        except urllib.error.HTTPError as err:
            err.close()
            return err.code

    def test_accepts_correlated_fire_and_resolve_per_alert(self):
        payload = self.payload()
        resolved = copy.deepcopy(payload["alerts"][0])
        resolved["status"] = "resolved"
        payload["alerts"].append(resolved)
        self.assertEqual(self.post(payload), 200)
        self.assertTrue(self.receiver.seen("faas-page", "firing", "baseline"))
        self.assertTrue(self.receiver.seen("faas-page", "resolved", "baseline"))

    def test_rejection_is_not_a_successful_receipt(self):
        self.receiver.reject_page = True
        self.assertEqual(self.post(self.payload()), 503)
        self.assertFalse(self.receiver.seen("faas-page", "firing", "baseline"))
        self.assertTrue(self.receiver.seen("faas-page", "firing", "baseline", accepted=False))

    def test_unrelated_alerts_are_rejected_atomically(self):
        payload = self.payload()
        wrong = copy.deepcopy(payload["alerts"][0])
        wrong["annotations"]["drill_id"] = "different-run"
        payload["alerts"].append(wrong)
        self.assertEqual(self.post(payload), 400)
        self.assertEqual(self.receiver.receipts, [])

    def test_invalid_payloads_do_not_create_receipts(self):
        cases = [[], {}, {"receiver": "wrong", "alerts": []}, {"receiver": "faas-page", "alerts": "invalid"},
                 {"receiver": "faas-page", "alerts": ["invalid"]}]
        for payload in cases:
            with self.subTest(payload=payload):
                self.assertEqual(self.post(payload), 400)
                self.assertEqual(self.receiver.receipts, [])


if __name__ == "__main__":
    unittest.main()
