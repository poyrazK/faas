import json

import pytest

from faas_sdk._rfc7807 import ErrCapacity, parse_problem, raise_for_problem


@pytest.mark.parametrize(
    "code",
    [
        "compute_region_admission_paused",
        "compute_region_unavailable",
        "compute_region_capacity_exhausted",
    ],
)
def test_regional_refusal_retains_region_and_capacity_sentinel(code):
    problem = parse_problem(
        json.dumps({"title": "Regional refusal", "status": 503, "code": code, "compute_region": "eu-fsn"}).encode(),
        "application/problem+json",
        503,
    )
    assert problem is not None
    assert problem.compute_region == "eu-fsn"
    with pytest.raises(ErrCapacity) as caught:
        raise_for_problem(problem)
    assert caught.value.problem.compute_region == "eu-fsn"
