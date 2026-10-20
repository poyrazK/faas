from faas_sdk.models.project_promotion_health import ProjectPromotionHealth


def test_worker_set_status_and_legacy_round_trip():
    workers = [
        {
            "app_id": name,
            "slug": name,
            "binding_id": name + "-binding",
            "binding_hash": "a" * 64,
            "previous_deployment_id": name + "-old",
            "drain_seconds": 30,
            "status": "building",
        }
        for name in ("billing", "mail", "webhooks")
    ]
    for multiple in (False, True):
        raw = {
            "policy": {"probes": [{"app_id": "api", "slug": "api", "method": "GET", "path": "/health"}]},
            "phase": "preflight",
            "generation": 2,
            "started_at": "2026-10-16T12:00:00Z",
            "deadline": "2026-10-16T12:10:00Z",
            "verdict": "unknown",
            "healthy_rounds": 0,
            "regressed_rounds": 0,
        }
        if multiple:
            raw["policy"]["workers"] = workers
            sample = {
                key: 0 for key in ("attempts", "failures", "incomplete", "duration_ms", "retries", "queue_delay_ms")
            }
            health = {
                "status": "insufficient_evidence",
                "candidate": sample,
                "baseline": sample,
                "pending_jobs": 0,
                "oldest_pending_age_ms": 0,
                "baseline_pending_jobs": 0,
                "baseline_oldest_pending_age_ms": 0,
            }
            raw["workers"] = [{"worker": worker, "health": health} for worker in workers]
        else:
            raw["policy"]["worker"] = workers[0]
            raw["worker"] = workers[0]
        parsed = ProjectPromotionHealth.from_dict(raw)
        encoded = parsed.to_dict()
        if multiple:
            assert "worker" not in encoded and "worker" not in encoded["policy"]
            assert encoded["policy"]["workers"] == workers
            assert [item["worker"]["app_id"] for item in encoded["workers"]] == ["billing", "mail", "webhooks"]
        else:
            assert "workers" not in encoded and "workers" not in encoded["policy"]
            assert encoded["worker"] == workers[0]
