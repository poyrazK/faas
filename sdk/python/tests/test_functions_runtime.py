import asyncio
import json

import httpx
import pytest

from faas_sdk.functions_runtime import FunctionCallError, Functions, function_base_url, functions_from_env


def test_functions_from_env_reads_the_deploy_map():
    assert functions_from_env({"GREGALE_FUNCTIONS": '{"users":"shop-users","x":1}'}) == {"users": "shop-users"}
    assert functions_from_env({"GREGALE_FUNCTIONS": "nope"}) == {}
    assert functions_from_env({}) == {}


def test_function_base_url_prefers_the_binding_url():
    assert function_base_url("shop-users", {}) == "http://shop-users.internal:10081"
    env = {"GREGALE_SERVICE_SHOP_USERS_URL": "https://shop-users.internal/"}
    assert function_base_url("shop-users", env) == "https://shop-users.internal"


def _recorder(responses):
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append(request)
        return responses.pop(0)

    return seen, httpx.MockTransport(handler)


def test_call_posts_json_over_the_binding():
    seen, transport = _recorder([httpx.Response(200, json={"id": "42", "name": "Ada"})])
    fns = Functions({"users": "shop-users"}, env={}, sync_transport=transport)
    assert fns.users.call_sync({"id": "42"}) == {"id": "42", "name": "Ada"}
    assert str(seen[0].url) == "http://shop-users.internal:10081/"
    assert seen[0].method == "POST"
    assert json.loads(seen[0].content) == {"id": "42"}


def test_async_send_asks_for_an_async_invocation():
    seen, transport = _recorder([httpx.Response(202, json={"invocation_id": "inv-1"})])
    fns = Functions({"mailer": "shop-mailer"}, env={}, async_transport=transport)
    sent = asyncio.run(fns.mailer.send({"to": "a@example.com"}))
    assert sent.invocation_id == "inv-1"
    assert seen[0].headers["x-gregale-invoke"] == "async"
    assert seen[0].headers["x-gregale-function-call"] == "1"


def test_a_non_2xx_answer_raises_with_the_problem():
    problem = {"code": "function_input_invalid", "detail": "/id: required", "status": 422}
    _, transport = _recorder([httpx.Response(422, json=problem)])
    fns = Functions({"users": "shop-users"}, env={}, sync_transport=transport)
    with pytest.raises(FunctionCallError) as err:
        fns["users"].call_sync({})
    assert err.value.status == 422 and err.value.code == "function_input_invalid"


def test_an_uncallable_name_says_what_is_callable():
    fns = Functions({"users": "shop-users"}, env={})
    with pytest.raises(AttributeError, match="callable: users"):
        _ = fns.billing


def test_send_names_the_completion_and_wait_long_polls():
    seen, transport = _recorder([
        httpx.Response(202, json={"id": "inv-2"}),
        httpx.Response(200, json={"id": "inv-2", "state": "dispatching", "done": False, "attempts": 1}),
        httpx.Response(200, json={"id": "inv-2", "state": "completed", "done": True, "attempts": 1, "result": {"sent": True}}),
    ])
    fns = Functions({"mailer": "shop-mailer"}, env={}, sync_transport=transport)
    sent = fns.mailer.send_sync({"to": "a@example.com"}, on_complete="self")
    assert seen[0].headers["x-gregale-on-complete"] == "self"
    assert sent.wait_sync(timeout=5) == {"sent": True}
    assert str(seen[1].url).startswith("http://api.svc.gregale:10081/v1/invocations/inv-2?wait=")


def test_wait_raises_for_a_failed_invocation():
    _, transport = _recorder([
        httpx.Response(202, json={"id": "inv-3"}),
        httpx.Response(200, json={"id": "inv-3", "state": "failed", "done": True, "attempts": 3, "error": "boom"}),
    ])
    fns = Functions({"mailer": "shop-mailer"}, env={}, async_transport=transport)

    async def run():
        sent = await fns.mailer.send({})
        await sent.wait()

    with pytest.raises(FunctionCallError) as err:
        asyncio.run(run())
    assert err.value.code == "function_invocation_failed"
