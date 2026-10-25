import tomllib
from pathlib import Path

PYPROJECT = Path(__file__).resolve().parents[1] / "pyproject.toml"


def test_distribution_name_is_not_the_unrelated_pypi_package():
    # `faas_sdk` / `faas-sdk` on PyPI is another company's package; installing
    # Gregale's SDK by that name would install their code (Hunt 13 H13-4).
    poetry = tomllib.loads(PYPROJECT.read_text())["tool"]["poetry"]
    assert poetry["name"] == "gregale-sdk"
    assert poetry["packages"] == [{"include": "faas_sdk"}]
