from faas_sdk.models.project_promotion_health import ProjectPromotionHealth


def test_queue_receipt_round_trip():
    raw = {
        "policy": {"probes": [{"app_id": "app", "slug": "api", "method": "GET", "path": "/health"}]},
        "phase": "queued",
        "generation": 1,
        "started_at": "2026-10-15T12:00:00Z",
        "deadline": "2026-10-15T12:15:00Z",
        "verdict": "unknown",
        "healthy_rounds": 0,
        "regressed_rounds": 0,
        "admission": {
            "queued_at": "2026-10-15T12:00:00Z",
            "wait_deadline": "2026-10-15T12:15:00Z",
            "reason": "fleet_capacity",
        },
    }
    h = ProjectPromotionHealth.from_dict(raw)
    assert h.phase == "queued"
    assert h.admission.reason == "fleet_capacity"
    encoded = h.to_dict()
    assert "started_at" not in encoded["admission"]
    assert encoded["admission"]["wait_deadline"].startswith("2026-10-15T12:15:00")
    raw["phase"] = "preflight"
    raw["admission"]["started_at"] = "2026-10-15T12:02:00Z"
    h = ProjectPromotionHealth.from_dict(raw)
    assert h.admission.started_at.minute == 2
