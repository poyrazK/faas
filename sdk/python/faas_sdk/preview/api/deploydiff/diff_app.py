from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.diff_request import DiffRequest
from ...models.diff_response import DiffResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: DiffRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/diff".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DiffResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DiffResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DiffResponse | Problem]:
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
    body: DiffRequest,
) -> Response[DiffResponse | Problem]:
    """Read-only preview of what a deploy would change.

     PR-1 of the deploy-diff cluster. Server-side equivalent of
    `gregale deploy --diff` — runs the same engine (pkg/deploydiff)
    and returns the same wire shape (DiffResponse).

    Read-only: no DB writes, no audit row, no deployment row. The
    app-not-found case is 200 with `diff.changes[]` containing a
    `would_create_app` row + the quota gate firing against the
    customer's plan — a CI consumer can preview a fresh deploy the
    same way the CLI does.

    Auth: bearer-key with `apps:read` (or admin). NO MFA on this
    endpoint, mirroring GET /v1/apps/{slug}/metrics.

    Schema-break detection is text-only in PR-1 (handler / entrypoint
    / env-key changes → warn-severity Breaks). Structural OpenAPI
    response-schema diff lands in PR-2.

    Args:
        slug (str):
        body (DiffRequest): JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
            purpose-built DTO — every field maps 1:1 to a
            [deploydiff.Pending] entry via the apid handler's adapter.
            Empty / absent fields mean "no change proposed" (matches
            the engine's pointer semantics: every nested field is
            optional; null = "don't touch").

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DiffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DiffRequest,
) -> DiffResponse | Problem | None:
    """Read-only preview of what a deploy would change.

     PR-1 of the deploy-diff cluster. Server-side equivalent of
    `gregale deploy --diff` — runs the same engine (pkg/deploydiff)
    and returns the same wire shape (DiffResponse).

    Read-only: no DB writes, no audit row, no deployment row. The
    app-not-found case is 200 with `diff.changes[]` containing a
    `would_create_app` row + the quota gate firing against the
    customer's plan — a CI consumer can preview a fresh deploy the
    same way the CLI does.

    Auth: bearer-key with `apps:read` (or admin). NO MFA on this
    endpoint, mirroring GET /v1/apps/{slug}/metrics.

    Schema-break detection is text-only in PR-1 (handler / entrypoint
    / env-key changes → warn-severity Breaks). Structural OpenAPI
    response-schema diff lands in PR-2.

    Args:
        slug (str):
        body (DiffRequest): JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
            purpose-built DTO — every field maps 1:1 to a
            [deploydiff.Pending] entry via the apid handler's adapter.
            Empty / absent fields mean "no change proposed" (matches
            the engine's pointer semantics: every nested field is
            optional; null = "don't touch").

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DiffResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DiffRequest,
) -> Response[DiffResponse | Problem]:
    """Read-only preview of what a deploy would change.

     PR-1 of the deploy-diff cluster. Server-side equivalent of
    `gregale deploy --diff` — runs the same engine (pkg/deploydiff)
    and returns the same wire shape (DiffResponse).

    Read-only: no DB writes, no audit row, no deployment row. The
    app-not-found case is 200 with `diff.changes[]` containing a
    `would_create_app` row + the quota gate firing against the
    customer's plan — a CI consumer can preview a fresh deploy the
    same way the CLI does.

    Auth: bearer-key with `apps:read` (or admin). NO MFA on this
    endpoint, mirroring GET /v1/apps/{slug}/metrics.

    Schema-break detection is text-only in PR-1 (handler / entrypoint
    / env-key changes → warn-severity Breaks). Structural OpenAPI
    response-schema diff lands in PR-2.

    Args:
        slug (str):
        body (DiffRequest): JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
            purpose-built DTO — every field maps 1:1 to a
            [deploydiff.Pending] entry via the apid handler's adapter.
            Empty / absent fields mean "no change proposed" (matches
            the engine's pointer semantics: every nested field is
            optional; null = "don't touch").

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DiffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: DiffRequest,
) -> DiffResponse | Problem | None:
    """Read-only preview of what a deploy would change.

     PR-1 of the deploy-diff cluster. Server-side equivalent of
    `gregale deploy --diff` — runs the same engine (pkg/deploydiff)
    and returns the same wire shape (DiffResponse).

    Read-only: no DB writes, no audit row, no deployment row. The
    app-not-found case is 200 with `diff.changes[]` containing a
    `would_create_app` row + the quota gate firing against the
    customer's plan — a CI consumer can preview a fresh deploy the
    same way the CLI does.

    Auth: bearer-key with `apps:read` (or admin). NO MFA on this
    endpoint, mirroring GET /v1/apps/{slug}/metrics.

    Schema-break detection is text-only in PR-1 (handler / entrypoint
    / env-key changes → warn-severity Breaks). Structural OpenAPI
    response-schema diff lands in PR-2.

    Args:
        slug (str):
        body (DiffRequest): JSON body for POST /v1/apps/{slug}/diff (PR-1). Slim
            purpose-built DTO — every field maps 1:1 to a
            [deploydiff.Pending] entry via the apid handler's adapter.
            Empty / absent fields mean "no change proposed" (matches
            the engine's pointer semantics: every nested field is
            optional; null = "don't touch").

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DiffResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
