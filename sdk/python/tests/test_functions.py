import pytest

from faas_sdk import function


def test_function_decorator_returns_handler_unchanged():
    @function(cron="0 8 * * *", ram_mb=512)
    def handler(event, ctx):
        return {"status": 200}

    assert handler({}, None) == {"status": 200}
    assert handler.__gregale_config__ == {"cron": "0 8 * * *", "ram_mb": 512}


def test_function_decorator_rejects_unknown_keys():
    with pytest.raises(TypeError, match="crn"):
        function(crn="0 8 * * *")
