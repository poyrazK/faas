"""Application probes use an independent HTTP client without SDK credentials."""

import json
from datetime import UTC, datetime

import httpx
from faas_sdk import VerificationCheck, VerificationCheckResult


def application_checks(target, application_url):
    url = httpx.URL(application_url)
    if (
        url.scheme not in ("http", "https")
        or url.username
        or url.password
        or url.fragment
    ):
        raise ValueError("Invalid application URL")
    worker = next(w for w in target.workloads if w.slug.endswith("-worker"))
    if not worker.deployment_id:
        raise ValueError("An explicit worker deployment is required")
    url = url.copy_merge_params({"worker_deployment_id": worker.deployment_id})

    async def probe(context):
        async with (
            httpx.AsyncClient(
                follow_redirects=False, timeout=context.remaining
            ) as client,
            client.stream(
                "GET", url, headers={"Accept": "application/json"}
            ) as response,
        ):
            data = bytearray()
            async for chunk in response.aiter_bytes(chunk_size=8192):
                if len(data) + len(chunk) > 16384:
                    return VerificationCheckResult("inconclusive")
                data.extend(chunk)
            if response.status_code != 200:
                return VerificationCheckResult("inconclusive")
        value = json.loads(data)
        if not context.slug.endswith("-worker"):
            return VerificationCheckResult(
                "verified"
                if value.get("ok") is True
                and value.get("deployment_id") == context.deployment_id
                else "inconclusive",
                value.get("deployment_id"),
            )
        observed = value.get("worker")
        if not observed or observed.get("deployment_id") != context.deployment_id:
            return VerificationCheckResult("inconclusive")
        age = (
            datetime.now(UTC).timestamp()
            - datetime.fromisoformat(observed["observed_at"]).timestamp()
        )
        if not 0 <= age <= 15:
            return VerificationCheckResult("inconclusive")
        return VerificationCheckResult(
            "failed"
            if observed.get("status") == "failed"
            else "verified"
            if observed.get("status") == "processed" and value.get("processed", 0) > 0
            else "inconclusive",
            observed["deployment_id"],
        )

    return tuple(VerificationCheck(w.slug, w.slug, probe) for w in target.workloads)
