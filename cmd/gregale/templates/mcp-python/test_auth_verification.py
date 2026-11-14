"""JWK purpose enforcement and bounded refresh attempts during provider outages."""
from concurrent.futures import ThreadPoolExecutor
import io
import json
import time
import unittest
from unittest.mock import Mock, patch
from urllib.error import URLError

import jwt
from cryptography.hazmat.primitives.asymmetric import ec, rsa

from auth import Authenticator, AuthFailure


CONFIG = {"endpoint": "/mcp", "auth": {
    "mode": "external-oauth", "issuer": "https://issuer.example",
    "jwks_url": "https://issuer.example/jwks", "resource": "https://mcp.example/mcp",
    "scopes": ["mcp:catalog"],
}}


def http_response(body):
    response = io.BytesIO(body)
    response.status = 200
    return response


class JWKVerificationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.private = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        cls.key = {**jwt.algorithms.RSAAlgorithm.to_jwk(cls.private.public_key(), as_dict=True), "kid": "provider-key"}

    def token(self, kid="provider-key", private=None, algorithm="RS256"):
        return jwt.encode({
            "iss": CONFIG["auth"]["issuer"], "aud": CONFIG["auth"]["resource"],
            "sub": "customer", "scope": "mcp:catalog", "exp": time.time() + 3600,
        }, private or self.private, algorithm=algorithm, headers={"kid": kid} if kid is not None else {})

    def status(self, auth, token):
        try:
            identity = auth.authenticate("Bearer " + token)
            self.assertEqual(identity.subject, "customer")
            return 200
        except AuthFailure as failure:
            return failure.status

    def test_selected_key_must_permit_verification(self):
        for operations, expected in ((["verify"], 200), (["encrypt"], 401), (["sign"], 401),
                                     ([], 401), (None, 401), ("verify", 401), (["verify", 1], 401)):
            with self.subTest(operations=operations):
                auth = Authenticator(CONFIG)
                with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [{**self.key, "key_ops": operations}]}):
                    self.assertEqual(self.status(auth, self.token()), expected)
        auth = Authenticator(CONFIG)
        with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [self.key]}):
            self.assertEqual(self.status(auth, self.token()), 200)

    def test_jwk_algorithm_must_match_token_algorithm(self):
        for algorithm, expected in (("RS256", 200), ("RS512", 401), (None, 401)):
            with self.subTest(algorithm=algorithm):
                auth = Authenticator(CONFIG)
                with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [{**self.key, "alg": algorithm}]}):
                    self.assertEqual(self.status(auth, self.token()), expected)

    def test_single_eligible_key_does_not_require_key_ids(self):
        for has_key_id in (False, True):
            with self.subTest(jwk_has_key_id=has_key_id):
                key = dict(self.key)
                if not has_key_id:
                    del key["kid"]
                auth = Authenticator(CONFIG)
                with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [key]}):
                    self.assertEqual(self.status(auth, self.token(kid=None)), 200)

    def test_omitted_id_filters_key_purpose_and_algorithm_before_checking_ambiguity(self):
        keys = [self.key, {**self.key, "kid": "encryption", "key_ops": ["encrypt"]},
                {**self.key, "kid": "other-algorithm", "alg": "RS512"}]
        auth = Authenticator(CONFIG)
        with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": keys}):
            self.assertEqual(self.status(auth, self.token(kid=None)), 200)

    def test_ambiguous_eligible_keys_are_rejected(self):
        for kid in (None, "provider-key"):
            with self.subTest(kid=kid):
                auth = Authenticator(CONFIG)
                with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [self.key, self.key]}):
                    self.assertEqual(self.status(auth, self.token(kid=kid)), 401)

    def test_es256_verification_without_key_ids(self):
        private = ec.generate_private_key(ec.SECP256R1())
        key = jwt.algorithms.ECAlgorithm.to_jwk(private.public_key(), as_dict=True)
        auth = Authenticator(CONFIG)
        with patch.object(auth.jwks_client, "fetch_data", return_value={"keys": [self.key, key]}):
            self.assertEqual(self.status(auth, self.token(kid=None, private=private, algorithm="ES256")), 200)

    def test_unknown_key_id_refreshes_after_cooldown_for_rotation(self):
        auth = Authenticator(CONFIG)
        opener = Mock()
        payload = {"keys": [self.key]}
        opener.open.side_effect = lambda *_args, **_kwargs: http_response(json.dumps(payload).encode())
        with patch("urllib.request.build_opener", return_value=opener), patch("auth.time.monotonic", return_value=1000) as clock:
            self.assertEqual(self.status(auth, self.token()), 200)
            self.assertEqual(self.status(auth, self.token("rotated-key")), 401)
            self.assertEqual(opener.open.call_count, 1)
            clock.return_value = 1030
            payload["keys"].append({**self.key, "kid": "rotated-key"})
            self.assertEqual(self.status(auth, self.token("rotated-key")), 200)
            self.assertEqual(opener.open.call_count, 2)

    def test_failed_fetches_cool_down_and_recover(self):
        for failure in (URLError("provider unavailable"), b"{", b'{"keys":null}'):
            with self.subTest(failure=failure):
                auth = Authenticator(CONFIG)
                token = self.token()
                opener = Mock()
                def fail(*_args, **_kwargs):
                    if isinstance(failure, Exception):
                        raise failure
                    return http_response(failure)
                opener.open.side_effect = fail
                with patch("urllib.request.build_opener", return_value=opener), patch("auth.time.monotonic", return_value=1000) as clock:
                    with ThreadPoolExecutor(max_workers=8) as workers:
                        self.assertEqual(list(workers.map(lambda _: self.status(auth, token), range(20))), [401] * 20)
                    for _ in range(20):
                        self.assertEqual(self.status(auth, token), 401)
                    self.assertEqual(opener.open.call_count, 1)
                    opener.open.side_effect = lambda *_args, **_kwargs: http_response(json.dumps({"keys": [self.key]}).encode())
                    clock.return_value = 1029.999
                    self.assertEqual(self.status(auth, token), 401)
                    self.assertEqual(opener.open.call_count, 1)
                    clock.return_value = 1030
                    self.assertEqual(self.status(auth, token), 200)
                    self.assertEqual(self.status(auth, token), 200)
                    self.assertEqual(opener.open.call_count, 2)

    def test_failed_rotation_preserves_fresh_cached_keys(self):
        auth = Authenticator(CONFIG)
        opener = Mock()
        opener.open.side_effect = lambda *_args, **_kwargs: http_response(json.dumps({"keys": [self.key]}).encode())
        with patch("urllib.request.build_opener", return_value=opener), patch("auth.time.monotonic", return_value=1000) as clock:
            self.assertEqual(self.status(auth, self.token()), 200)
            clock.return_value = 1031
            opener.open.side_effect = URLError("provider unavailable")
            self.assertEqual(self.status(auth, self.token("rotated-key")), 401)
            for _ in range(5):
                self.assertEqual(self.status(auth, self.token()), 200)
                self.assertEqual(self.status(auth, self.token("rotated-key")), 401)
            self.assertEqual(opener.open.call_count, 2)
