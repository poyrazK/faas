import pytest

from faas_sdk import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest


def test_target_digest_matches_gateway_format_and_node_vectors():
    assert PRE_AUTH_TARGET_HEADER == "X-Gregale-Abuse-Target"
    assert pre_auth_target_digest(bytes([0x0B]) * 32, "Hi There") == (
        "198a607eb44bfbc69903a0f1cf2bbdc5ba0aa3f3d9ae3c1c7a3b1696a0b68cf7"
    )
    assert pre_auth_target_digest("a" * 32, "é@example.com") == (
        "b0759e2dbe2de6f20fca64ac31d0dcd0828401f3d62ae4146267607dd77cb52a"
    )


def test_caller_controls_normalization_and_errors_do_not_reveal_inputs():
    secret = "private-app-owned-secret-that-is-long-enough"
    assert pre_auth_target_digest(secret, "user@example.com") != pre_auth_target_digest(secret, "User@example.com")
    assert pre_auth_target_digest(secret, "user@example.com") != pre_auth_target_digest(secret, " user@example.com ")
    with pytest.raises(ValueError, match="at least 32 bytes") as exc:
        pre_auth_target_digest("short", "user@example.com")
    assert "short" not in str(exc.value)
    assert "user@example.com" not in str(exc.value)
    with pytest.raises(TypeError, match="nonempty string"):
        pre_auth_target_digest(secret, "")
    with pytest.raises(ValueError, match="valid Unicode"):
        pre_auth_target_digest(secret, "\ud800")
