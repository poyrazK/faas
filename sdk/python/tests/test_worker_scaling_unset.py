from faas_sdk.models import WorkerScaling
from faas_sdk.types import Unset


def test_worker_scaling_unset_instances_do_not_become_wire_values() -> None:
    model = WorkerScaling(min_=0, max_=5, metric="queue_depth", target=100, name=Unset())
    assert "name" not in model.to_dict()
