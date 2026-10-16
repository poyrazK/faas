import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("qualification", Path(__file__).with_name("project-http-qualification.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
SHA = "a" * 40


def evidence():
    lines = []
    for name, (rollback, stabilize, status) in module.SCENARIOS.items():
        lines.extend([f"=== RUN   {name}",
                      f"    fixture.go:1: progressive native: attempt={'b' * 32} stages=5,25,100 restart=true rollback={str(rollback).lower()} stabilization={str(stabilize).lower()} status={status} http_only=true",
                      f"--- PASS: {name} (1.00s)"])
    for name, (scenario, replay, status) in module.FAILURE_SCENARIOS.items():
        lines.extend([f"=== RUN   {name}",
                      f"    fixture.go:1: progressive failure native: attempt={'b' * 32} scenario={scenario} restart=true replay={str(replay).lower()} status={status} http_only=true",
                      f"--- PASS: {name} (1.00s)"])
    for name, (scenario, status) in module.SERVICE_SCENARIOS.items():
        stages = "" if scenario == "abort" else "stages=5,25,100 "
        lines.extend([f"=== RUN   {name}",
                      f"    fixture.go:1: service graph native: scenario={scenario} {stages}restart=true downstream=true status={status}",
                      f"--- PASS: {name} (1.00s)"])
    lines.extend(["native HTTP qualification: final_leakcheck_rc=0",
                  f"native HTTP qualification: sha={SHA} final_rc=0"])
    return "\n".join(lines)


class QualificationTests(unittest.TestCase):
    def test_complete(self):
        self.assertTrue(module.qualify(evidence(), SHA)["qualified"])

    def test_secret_scope(self):
        lines = []
        for name, expected in module.SECRET_SCENARIOS.items():
            lines.extend([f"=== RUN   {name}", f"    fixture.go:1: release secrets native: {expected}", f"--- PASS: {name} (1s)"])
        lines.extend(["native HTTP qualification: final_leakcheck_rc=0", f"native HTTP qualification: sha={SHA} final_rc=0"])
        log = "\n".join(lines)
        scope = "project-release-secrets-only"
        self.assertTrue(module.qualify(log, SHA, scope)["qualified"])
        for original, changed in [("rekey=true", "rekey=false"), ("old_key_removed=true", "old_key_removed=false"), ("cold_boot=true", "cold_boot=false"), ("encrypted=true", "encrypted=false"), ("secret_config=matched", "secret_config=mixed"), ("restart=true", "restart=false")]:
            self.assertFalse(module.qualify(log.replace(original, changed), SHA, scope)["qualified"])
        for name in module.SECRET_SCENARIOS:
            for verdict in ("SKIP", "FAIL"):
                self.assertFalse(module.qualify(log.replace(f"--- PASS: {name}", f"--- {verdict}: {name}"), SHA, scope)["qualified"])
        self.assertFalse(module.qualify(evidence(), SHA, scope)["qualified"])

    def test_retention_scope(self):
        lines = []
        for name, expected in module.RETENTION_SCENARIOS.items():
            lines.extend([f"=== RUN   {name}", f"    fixture.go:1: rollback retention native: {expected}", f"--- PASS: {name} (1s)"])
        lines.extend(["native HTTP qualification: final_leakcheck_rc=0", f"native HTTP qualification: sha={SHA} final_rc=0"])
        log = "\n".join(lines)
        scope = "project-rollback-retention-only"
        self.assertTrue(module.qualify(log, SHA, scope)["qualified"])
        for original, changed in [("cold_boot=true", "cold_boot=false"), ("clock=real", "clock=edited"), ("recovery=denied", "recovery=accepted"), ("graph_config=preserved", "graph_config=changed")]:
            self.assertFalse(module.qualify(log.replace(original, changed), SHA, scope)["qualified"])
        for name in module.RETENTION_SCENARIOS:
            for verdict in ("SKIP", "FAIL"):
                self.assertFalse(module.qualify(log.replace(f"--- PASS: {name}", f"--- {verdict}: {name}"), SHA, scope)["qualified"])
        self.assertFalse(module.qualify(evidence(), SHA, scope)["qualified"])
        self.assertFalse(module.qualify(log, SHA)["qualified"])

    def test_concurrent_scope(self):
        lines = []
        for name, (scenario, percent) in module.CONCURRENT_SCENARIOS.items():
            lines.extend([f"=== RUN   {name}",
                          f"    fixture.go:1: concurrent deploy native: scenario={scenario} percent={percent} restart=true stale_controls=denied winner_preserved=true status=conflicted",
                          f"--- PASS: {name} (1s)"])
        lines.extend(["native HTTP qualification: final_leakcheck_rc=0",
                      f"native HTTP qualification: sha={SHA} final_rc=0"])
        log = "\n".join(lines)
        scope = "project-concurrent-release-only"
        self.assertTrue(module.qualify(log, SHA, scope)["qualified"])
        for original, changed in [("winner_preserved=true", "winner_preserved=false"),
                                  ("stale_controls=denied", "stale_controls=accepted"),
                                  ("percent=100", "percent=5"),
                                  ("restart=true", "restart=false")]:
            self.assertFalse(module.qualify(log.replace(original, changed, 1), SHA, scope)["qualified"])
        for name in module.CONCURRENT_SCENARIOS:
            for verdict in ("FAIL", "SKIP"):
                self.assertFalse(module.qualify(log.replace(f"--- PASS: {name}", f"--- {verdict}: {name}"), SHA, scope)["qualified"])
        self.assertFalse(module.qualify(evidence(), SHA, scope)["qualified"])
        self.assertFalse(module.qualify(log, SHA)["qualified"])
        self.assertFalse(module.qualify(log, SHA, "unknown")["qualified"])

    def test_every_scenario_is_required(self):
        for name in module.SCENARIOS | module.FAILURE_SCENARIOS | module.SERVICE_SCENARIOS:
            with self.subTest(name=name):
                for verdict in ("SKIP", "FAIL"):
                    log = evidence().replace(f"--- PASS: {name}", f"--- {verdict}: {name}")
                    self.assertFalse(module.qualify(log, SHA)["qualified"])
                log = "\n".join(line for line in evidence().splitlines() if name not in line)
                self.assertFalse(module.qualify(log, SHA)["qualified"])

    def test_incomplete_or_wrong_evidence(self):
        base = evidence()
        changes = [base.replace("--- PASS:", "--- SKIP:", 1),
                   base.replace("--- PASS:", "--- FAIL:", 1),
                   base.replace("stages=5,25,100", "stages=5,100", 1),
                   base.replace("http_only=true", "http_only=false", 1),
                   base.replace("restart=true", "restart=false", 1),
                   base.replace("final_rc=0", "final_rc=1"),
                   base.replace("final_leakcheck_rc=0", "final_leakcheck_rc=1"),
                   base.replace(SHA, "c" * 40), "",
                   base + "\nnative HTTP qualification: final_leakcheck_rc=0",
                   base.replace("downstream=true", "downstream=false", 1),
                   base.replace("replay=true", "replay=false", 1),
                   base.replace("scenario=full_recover", "scenario=early_abort"),
                   base + "\n--- FAIL: OtherMetal (1s)",
                   base.replace("=== RUN   TestProjectProgressiveHTTPPromotionMetal", "=== RUN   OtherMetal")]
        for log in changes:
            with self.subTest(log=log):
                self.assertFalse(module.qualify(log, SHA)["qualified"])


if __name__ == "__main__":
    unittest.main()
