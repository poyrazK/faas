from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_streaming_status import AppStreamingStatus
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    host: str | Unset = UNSET,
    path: str | Unset = UNSET,
    method: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["host"] = host

    params["path"] = path

    params["method"] = method

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/streaming-cap".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppStreamingStatus | Problem | None:
    if response.status_code == 200:
        response_200 = AppStreamingStatus.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppStreamingStatus | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    host: str | Unset = UNSET,
    path: str | Unset = UNSET,
    method: str | Unset = UNSET,
) -> Response[AppStreamingStatus | Problem]:
    r"""Per-app streaming classification probe (ADR-102 D6).

     Returns the streaming-status enum (one of `streaming`,
    `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
    `operator-disabled`, `upgrade-bypass`) the gatewayd handler
    would stamp on the `Streaming-Status` response header for a
    representative request to this app, plus the effective
    response-body cap (in bytes) and the per-gate flags.

    With no query parameters the probe is a pure read against the
    apid cache (the per-account `Plan` and the per-app
    `streaming_enabled` flag). Supplying `host`, `path`, and `method`
    together performs a bounded loopback read of gatewayd's compiled
    kind=limit rules and reports a matching streaming response cap with
    `cap_kind=\"endpoint-rule\"`. If gatewayd is unavailable or no rule
    matches, the response falls back to the plan cap.

    The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
    gatewayd-side state, so the canonical signal is the
    `Streaming-Status` response header on a real request, not this probe.
    A customer evaluating \"will my next request stream?\" must
    consider the operator-side flag separately.

    `status=plan-disallows` means the customer's plan tier
    forbids `streaming_enabled=true`; the CreateApp gate (D5)
    already returns 403 `CodePlanStreamingNotAllowed` so this
    row should be unreachable from a properly-validated app,
    but the probe still reflects the persisted state for
    audits and pinned-SDK migrations.

    Args:
        slug (str):
        host (str | Unset):
        path (str | Unset):
        method (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStreamingStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        host=host,
        path=path,
        method=method,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    host: str | Unset = UNSET,
    path: str | Unset = UNSET,
    method: str | Unset = UNSET,
) -> AppStreamingStatus | Problem | None:
    r"""Per-app streaming classification probe (ADR-102 D6).

     Returns the streaming-status enum (one of `streaming`,
    `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
    `operator-disabled`, `upgrade-bypass`) the gatewayd handler
    would stamp on the `Streaming-Status` response header for a
    representative request to this app, plus the effective
    response-body cap (in bytes) and the per-gate flags.

    With no query parameters the probe is a pure read against the
    apid cache (the per-account `Plan` and the per-app
    `streaming_enabled` flag). Supplying `host`, `path`, and `method`
    together performs a bounded loopback read of gatewayd's compiled
    kind=limit rules and reports a matching streaming response cap with
    `cap_kind=\"endpoint-rule\"`. If gatewayd is unavailable or no rule
    matches, the response falls back to the plan cap.

    The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
    gatewayd-side state, so the canonical signal is the
    `Streaming-Status` response header on a real request, not this probe.
    A customer evaluating \"will my next request stream?\" must
    consider the operator-side flag separately.

    `status=plan-disallows` means the customer's plan tier
    forbids `streaming_enabled=true`; the CreateApp gate (D5)
    already returns 403 `CodePlanStreamingNotAllowed` so this
    row should be unreachable from a properly-validated app,
    but the probe still reflects the persisted state for
    audits and pinned-SDK migrations.

    Args:
        slug (str):
        host (str | Unset):
        path (str | Unset):
        method (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppStreamingStatus | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        host=host,
        path=path,
        method=method,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    host: str | Unset = UNSET,
    path: str | Unset = UNSET,
    method: str | Unset = UNSET,
) -> Response[AppStreamingStatus | Problem]:
    r"""Per-app streaming classification probe (ADR-102 D6).

     Returns the streaming-status enum (one of `streaming`,
    `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
    `operator-disabled`, `upgrade-bypass`) the gatewayd handler
    would stamp on the `Streaming-Status` response header for a
    representative request to this app, plus the effective
    response-body cap (in bytes) and the per-gate flags.

    With no query parameters the probe is a pure read against the
    apid cache (the per-account `Plan` and the per-app
    `streaming_enabled` flag). Supplying `host`, `path`, and `method`
    together performs a bounded loopback read of gatewayd's compiled
    kind=limit rules and reports a matching streaming response cap with
    `cap_kind=\"endpoint-rule\"`. If gatewayd is unavailable or no rule
    matches, the response falls back to the plan cap.

    The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
    gatewayd-side state, so the canonical signal is the
    `Streaming-Status` response header on a real request, not this probe.
    A customer evaluating \"will my next request stream?\" must
    consider the operator-side flag separately.

    `status=plan-disallows` means the customer's plan tier
    forbids `streaming_enabled=true`; the CreateApp gate (D5)
    already returns 403 `CodePlanStreamingNotAllowed` so this
    row should be unreachable from a properly-validated app,
    but the probe still reflects the persisted state for
    audits and pinned-SDK migrations.

    Args:
        slug (str):
        host (str | Unset):
        path (str | Unset):
        method (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppStreamingStatus | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        host=host,
        path=path,
        method=method,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    host: str | Unset = UNSET,
    path: str | Unset = UNSET,
    method: str | Unset = UNSET,
) -> AppStreamingStatus | Problem | None:
    r"""Per-app streaming classification probe (ADR-102 D6).

     Returns the streaming-status enum (one of `streaming`,
    `accept-json-downgrade`, `flag-disabled`, `plan-disallows`,
    `operator-disabled`, `upgrade-bypass`) the gatewayd handler
    would stamp on the `Streaming-Status` response header for a
    representative request to this app, plus the effective
    response-body cap (in bytes) and the per-gate flags.

    With no query parameters the probe is a pure read against the
    apid cache (the per-account `Plan` and the per-app
    `streaming_enabled` flag). Supplying `host`, `path`, and `method`
    together performs a bounded loopback read of gatewayd's compiled
    kind=limit rules and reports a matching streaming response cap with
    `cap_kind=\"endpoint-rule\"`. If gatewayd is unavailable or no rule
    matches, the response falls back to the plan cap.

    The operator opt-in (`FAAS_GATEWAY_STREAMING` env) remains
    gatewayd-side state, so the canonical signal is the
    `Streaming-Status` response header on a real request, not this probe.
    A customer evaluating \"will my next request stream?\" must
    consider the operator-side flag separately.

    `status=plan-disallows` means the customer's plan tier
    forbids `streaming_enabled=true`; the CreateApp gate (D5)
    already returns 403 `CodePlanStreamingNotAllowed` so this
    row should be unreachable from a properly-validated app,
    but the probe still reflects the persisted state for
    audits and pinned-SDK migrations.

    Args:
        slug (str):
        host (str | Unset):
        path (str | Unset):
        method (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppStreamingStatus | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            host=host,
            path=path,
            method=method,
        )
    ).parsed
