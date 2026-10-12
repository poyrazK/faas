"""External OAuth resource-server checks for the Gregale MCP starter."""

from urllib.parse import urlsplit, urlunsplit
import re

import jwt


SCOPE_PATTERN = re.compile(r"^[\x21\x23-\x5b\x5d-\x7e]+$")
CONTROL_PATTERN = re.compile(r"[\x00-\x20\x7f]")
ENDPOINT_PATTERN = re.compile(r"^/(?:[A-Za-z0-9._~-]+/?)*$")
BEARER_PATTERN = re.compile(r"(?i:Bearer) +([^ ]+)")
RESOURCE_VARIABLE_PATTERN = re.compile(r"\{([A-Za-z][A-Za-z0-9_.-]*)\}")
MAX_BEARER_TOKEN_BYTES = 32 * 1024


class AuthConfigurationError(ValueError):
    pass


class AuthFailure(Exception):
    def __init__(self, status, code, scopes=None, challenge=True):
        super().__init__(code)
        self.status = status
        self.code = code
        self.scopes = list(scopes or [])
        self.challenge = challenge


class AuthContext(set):
    """Set-compatible verified scopes plus the caller key used by durable Tasks."""

    def __init__(self, scopes=(), subject="", client_id="", resource="", expires_at=None):
        super().__init__(scopes)
        self.subject = subject
        self.client_id = client_id
        self.resource = resource
        self.expires_at = expires_at


def validate_scopes(value, label):
    if not isinstance(value, list) or any(
        not isinstance(scope, str) or not SCOPE_PATTERN.fullmatch(scope)
        for scope in value
    ):
        raise AuthConfigurationError(f"{label} must be an array of nonempty ASCII scope tokens")
    return value


def validate_https_url(value, label):
    if not isinstance(value, str) or not value:
        raise AuthConfigurationError(f"auth.{label} must be an HTTPS URL")
    try:
        parsed = urlsplit(value)
        _ = parsed.port  # Validate a numeric port in the allowed range.
    except ValueError:
        raise AuthConfigurationError(f"auth.{label} must be an HTTPS URL") from None
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or "?" in value
        or "#" in value
    ):
        raise AuthConfigurationError(f"auth.{label} must be HTTPS without credentials, query, or fragment")
    return parsed


def canonical_resource_identity(parsed):
    host = parsed.hostname.lower()
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"
    try:
        port = parsed.port
    except ValueError:
        return parsed.geturl()
    if port is not None and port != 443:
        host = f"{host}:{port}"
    return urlunsplit(("https", host, parsed.path or "/", "", ""))


def compile_resource_rule(pattern, scopes):
    if not isinstance(pattern, str) or not pattern or CONTROL_PATTERN.search(pattern):
        raise AuthConfigurationError("resource_scopes keys must be absolute resource URIs or simple URI templates")
    variables = list(RESOURCE_VARIABLE_PATTERN.finditer(pattern))
    expanded = RESOURCE_VARIABLE_PATTERN.sub("placeholder", pattern)
    if "{" in expanded or "}" in expanded:
        raise AuthConfigurationError("resource_scopes keys must be valid absolute resource URIs or URI templates")
    try:
        parsed = urlsplit(expanded)
    except ValueError:
        raise AuthConfigurationError("resource_scopes keys must be valid absolute resource URIs or URI templates") from None
    if not parsed.scheme or parsed.username is not None or parsed.password is not None or parsed.fragment or "#" in expanded:
        raise AuthConfigurationError("resource_scopes keys must be absolute resource URIs without credentials or fragments")
    if not variables:
        return pattern, list(scopes), None
    expression = []
    position = 0
    for variable in variables:
        expression.append(re.escape(pattern[position:variable.start()]))
        expression.append(r"([^/?#]+)")
        position = variable.end()
    expression.append(re.escape(pattern[position:]))
    return pattern, list(scopes), re.compile("^" + "".join(expression) + "$")


def parse_scope_policy(auth, field, mode, resource=False):
    configured = field in auth
    value = auth.get(field)
    if not configured:
        value = {}
    if not isinstance(value, dict):
        raise AuthConfigurationError(f"{field} must be an object")
    rules = {}
    templates = []
    for name, scopes in value.items():
        if resource:
            exact_name, parsed_scopes, matcher = compile_resource_rule(name, validate_scopes(scopes, f"{field} values"))
            rules[exact_name] = parsed_scopes
            if matcher is not None:
                templates.append((exact_name, parsed_scopes, matcher))
        else:
            if not isinstance(name, str) or not name or CONTROL_PATTERN.search(name):
                raise AuthConfigurationError(f"{field} keys must be nonempty names without whitespace or controls")
            rules[name] = validate_scopes(scopes, f"{field} values")
        if mode == "open" and scopes:
            raise AuthConfigurationError(f"scoped {field} entries require external-oauth")
    return {"configured": configured, "rules": rules, "templates": templates}


def required_policy_scopes(policy, identifier):
    if identifier in policy["rules"]:
        return policy["rules"][identifier], True
    if not policy["configured"]:
        return [], True
    required = set()
    matched = False
    for _pattern, scopes, matcher in policy["templates"]:
        if matcher.fullmatch(identifier):
            matched = True
            required.update(scopes)
    return (sorted(required), True) if matched else (None, False)


class Authenticator:
    def __init__(self, config):
        if not isinstance(config, dict) or not isinstance(config.get("auth"), dict):
            raise AuthConfigurationError("auth settings are required")
        auth = config["auth"]
        self.mode = auth.get("mode")
        if not isinstance(self.mode, str) or self.mode not in {"open", "external-oauth"}:
            raise AuthConfigurationError("auth.mode must be open or external-oauth")
        self.endpoint = config.get("endpoint")
        if (
            not isinstance(self.endpoint, str)
            or not ENDPOINT_PATTERN.fullmatch(self.endpoint)
            or "//" in self.endpoint
            or any(part in {".", ".."} for part in self.endpoint.split("/"))
        ):
            raise AuthConfigurationError("invalid MCP endpoint")
        self.base_scopes = auth.get("scopes", [])
        if self.base_scopes is None and self.mode == "open":
            self.base_scopes = []
        validate_scopes(self.base_scopes, "auth.scopes")
        self.tool_policy = parse_scope_policy(auth, "tool_scopes", self.mode)
        self.resource_policy = parse_scope_policy(auth, "resource_scopes", self.mode, resource=True)
        self.prompt_policy = parse_scope_policy(auth, "prompt_scopes", self.mode)
        self.metadata_url = None
        self.metadata_path = None
        self.jwks_client = None
        self.resource_identity = ""

        if self.mode == "open":
            if any(auth.get(key) for key in ("issuer", "jwks_url", "resource")) or self.base_scopes:
                raise AuthConfigurationError("open auth must not contain OAuth settings")
            return

        issuer = validate_https_url(auth.get("issuer"), "issuer")
        jwks_url = validate_https_url(auth.get("jwks_url"), "jwks_url")
        resource = validate_https_url(auth.get("resource"), "resource")
        if resource.path != self.endpoint:
            raise AuthConfigurationError("auth.resource must identify the MCP endpoint")
        if not self.base_scopes:
            raise AuthConfigurationError("external-oauth requires nonempty auth.scopes")

        self.issuer = auth["issuer"]
        self.resource = auth["resource"]
        self.resource_identity = canonical_resource_identity(resource)
        self.jwks_url = auth["jwks_url"]
        self.metadata_path = "/.well-known/oauth-protected-resource" + self.endpoint
        self.metadata_url = urlunsplit((
            resource.scheme,
            resource.netloc,
            self.metadata_path,
            "",
            "",
        ))
        try:
            from jwt import PyJWKClient
        except ImportError as exc:
            raise AuthConfigurationError("install requirements.txt to enable external OAuth") from exc
        self.jwks_client = PyJWKClient(
            self.jwks_url,
            cache_jwk_set=True,
            lifespan=300,
            timeout=5,
            headers={"Accept": "application/json"},
        )

    def metadata(self):
        if self.mode != "external-oauth":
            return None
        scopes = set(self.base_scopes)
        for policy in (self.tool_policy, self.resource_policy, self.prompt_policy):
            for required in policy["rules"].values():
                scopes.update(required)
        return {
            "resource": self.resource,
            "authorization_servers": [self.issuer],
            "scopes_supported": sorted(scopes),
            "bearer_methods_supported": ["header"],
        }

    def authenticate(self, authorization):
        if self.mode == "open":
            return AuthContext(resource="")
        match = BEARER_PATTERN.fullmatch(authorization or "")
        if not match or len(match.group(1).encode("utf-8")) > MAX_BEARER_TOKEN_BYTES:
            raise AuthFailure(401, "authentication_required", self.base_scopes)
        token = match.group(1)
        try:
            header = jwt.get_unverified_header(token)
            if not isinstance(header, dict) or header.get("crit") or (
                "b64" in header and header["b64"] is not True
            ):
                raise ValueError("unsupported JWT header")
            signing_key = self.jwks_client.get_signing_key_from_jwt(token).key
            from cryptography.hazmat.primitives.asymmetric import ec, rsa

            if isinstance(signing_key, rsa.RSAPublicKey):
                if signing_key.key_size < 2048 or signing_key.key_size > 8192:
                    raise ValueError("unsupported RSA key size")
            elif isinstance(signing_key, ec.EllipticCurvePublicKey):
                if not isinstance(signing_key.curve, ec.SECP256R1):
                    raise ValueError("unsupported EC curve")
            else:
                raise ValueError("unsupported JWT signing key")
            claims = jwt.decode(
                token,
                signing_key,
                algorithms=["RS256", "ES256"],
                issuer=self.issuer,
                audience=self.resource,
                options={"require": ["exp", "sub"]},
            )
            subject = claims.get("sub")
            if not isinstance(subject, str) or not subject:
                raise ValueError("invalid subject")
        except Exception:
            raise AuthFailure(401, "invalid_token", self.base_scopes) from None

        scope_claim = claims.get("scope")
        granted = set(scope_claim.split(" ")) if isinstance(scope_claim, str) else set()
        if not set(self.base_scopes).issubset(granted):
            raise AuthFailure(403, "insufficient_scope", self.base_scopes)
        return AuthContext(
            granted,
            subject=subject,
            client_id=claims.get("client_id", "") if isinstance(claims.get("client_id", ""), str) else "",
            resource=self.resource_identity,
            expires_at=claims.get("exp"),
        )

    def access_failure(self, policy, identifier, kind, granted_scopes):
        required, listed = required_policy_scopes(policy, identifier)
        if not listed:
            return AuthFailure(403, f"{kind}_access_denied", challenge=False)
        if self.mode == "external-oauth" and not set(required).issubset(granted_scopes):
            scopes = list(dict.fromkeys(self.base_scopes + required))
            return AuthFailure(403, "insufficient_scope", scopes)
        return None

    def tool_failure(self, name, granted_scopes):
        return self.access_failure(self.tool_policy, name, "tool", granted_scopes)

    def resource_failure(self, uri, granted_scopes):
        return self.access_failure(self.resource_policy, uri, "resource", granted_scopes)

    def prompt_failure(self, name, granted_scopes):
        return self.access_failure(self.prompt_policy, name, "prompt", granted_scopes)

    def can_access_tool(self, name, granted_scopes):
        return self.tool_failure(name, granted_scopes) is None

    def can_access_resource(self, uri, granted_scopes):
        return self.resource_failure(uri, granted_scopes) is None

    def can_access_prompt(self, name, granted_scopes):
        return self.prompt_failure(name, granted_scopes) is None

    def authorize_request(self, method, params, granted_scopes):
        if not isinstance(params, dict):
            return None
        name = params.get("name")
        if method == "tools/call" and isinstance(name, str):
            return self.tool_failure(name, granted_scopes)
        if method == "resources/read":
            uri = params.get("uri")
            if isinstance(uri, str):
                return self.resource_failure(uri, granted_scopes)
        if method == "prompts/get" and isinstance(name, str):
            return self.prompt_failure(name, granted_scopes)
        if method == "completion/complete":
            reference = params.get("ref")
            if not isinstance(reference, dict):
                return None
            reference_type = reference.get("type")
            if reference_type == "ref/prompt" and isinstance(reference.get("name"), str):
                return self.prompt_failure(reference["name"], granted_scopes)
            if reference_type == "ref/resource" and isinstance(reference.get("uri"), str):
                return self.resource_failure(reference["uri"], granted_scopes)
        return None
