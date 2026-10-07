from faas_sdk.models import WorkerScaling
from faas_sdk.types import Unset


def test_worker_custom_metric_name_round_trips() -> None:
    model = WorkerScaling(
        min_=1,
        max_=10,
        metric="custom",
        name="mcp_tasks_outstanding",
        target=4.0,
    )

    payload = model.to_dict()
    assert payload["metric"] == "custom"
    assert payload["name"] == "mcp_tasks_outstanding"
    assert payload["target"] == 4.0

    restored = WorkerScaling.from_dict(payload)
    assert restored == model


def test_worker_scaling_name_remains_optional_for_queue_metrics() -> None:
    model = WorkerScaling.from_dict({"min": 0, "max": 5, "metric": "queue_depth", "target": 100})

    assert isinstance(model.name, Unset)
    assert "name" not in model.to_dict()
