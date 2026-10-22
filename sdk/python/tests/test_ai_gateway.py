import pytest

from faas_sdk import (
    AI_SESSION_HEADER,
    AI_USER_HEADER,
    AIGatewayError,
    ai_gateway_headers,
    ai_gateway_url,
    is_valid_ai_user_id,
    opaque_ai_user_id,
)


def test_ai_gateway_url_addresses_an_integration_or_a_route():
    env = {"GREGALE_OUTBOUND_URL": "http://127.0.0.1:2774/"}
    assert ai_gateway_url(integration="int-1", env=env) == "http://127.0.0.1:2774/i/int-1"
    assert ai_gateway_url(route="route-1", env=env) == "http://127.0.0.1:2774/r/route-1"
    for kwargs in (
        {"route": "route-1", "env": {}},
        {"env": env},
        {"integration": "a", "route": "b", "env": env},
        {"route": "a/b", "env": env},
    ):
        with pytest.raises(AIGatewayError):
            ai_gateway_url(**kwargs)


def test_ai_gateway_headers_validate_ids_like_the_gateway():
    headers = ai_gateway_headers(user="user:42", session="chat-7")
    assert headers == {AI_USER_HEADER: "user:42", AI_SESSION_HEADER: "chat-7"}
    assert ai_gateway_headers() == {}
    for bad in ("", "has space", "x" * 65, "~other"):
        assert not is_valid_ai_user_id(bad)
        with pytest.raises(AIGatewayError):
            ai_gateway_headers(user=bad)


def test_opaque_ai_user_id_is_stable_valid_and_keyed():
    plain = opaque_ai_user_id("alice@example.com")
    keyed = opaque_ai_user_id("alice@example.com", "secret")
    assert plain == opaque_ai_user_id("alice@example.com")
    assert keyed == opaque_ai_user_id("alice@example.com", b"secret")
    assert keyed != plain
    assert is_valid_ai_user_id(plain) and is_valid_ai_user_id(keyed)
