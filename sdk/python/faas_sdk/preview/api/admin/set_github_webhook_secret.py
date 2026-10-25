from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.admin_set_github_webhook_secret_request import AdminSetGithubWebhookSecretRequest
from ...models.admin_set_github_webhook_secret_response import AdminSetGithubWebhookSecretResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AdminSetGithubWebhookSecretRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/admin/github-webhook-secrets",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AdminSetGithubWebhookSecretResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AdminSetGithubWebhookSecretResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AdminSetGithubWebhookSecretResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AdminSetGithubWebhookSecretRequest,
) -> Response[AdminSetGithubWebhookSecretResponse | Problem]:
    """Set the per-tenant GitHub App webhook secret (admin-only).

    Args:
        body (AdminSetGithubWebhookSecretRequest): Body shape for POST /v1/admin/github-webhook-
            secrets
            (PR-D / ADR-012 §7 amendment). The CLI takes hex so the
            plaintext never has to be a binary argv value; the apid
            handler hex-decodes before the INSERT.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminSetGithubWebhookSecretResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: AdminSetGithubWebhookSecretRequest,
) -> AdminSetGithubWebhookSecretResponse | Problem | None:
    """Set the per-tenant GitHub App webhook secret (admin-only).

    Args:
        body (AdminSetGithubWebhookSecretRequest): Body shape for POST /v1/admin/github-webhook-
            secrets
            (PR-D / ADR-012 §7 amendment). The CLI takes hex so the
            plaintext never has to be a binary argv value; the apid
            handler hex-decodes before the INSERT.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminSetGithubWebhookSecretResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AdminSetGithubWebhookSecretRequest,
) -> Response[AdminSetGithubWebhookSecretResponse | Problem]:
    """Set the per-tenant GitHub App webhook secret (admin-only).

    Args:
        body (AdminSetGithubWebhookSecretRequest): Body shape for POST /v1/admin/github-webhook-
            secrets
            (PR-D / ADR-012 §7 amendment). The CLI takes hex so the
            plaintext never has to be a binary argv value; the apid
            handler hex-decodes before the INSERT.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AdminSetGithubWebhookSecretResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AdminSetGithubWebhookSecretRequest,
) -> AdminSetGithubWebhookSecretResponse | Problem | None:
    """Set the per-tenant GitHub App webhook secret (admin-only).

    Args:
        body (AdminSetGithubWebhookSecretRequest): Body shape for POST /v1/admin/github-webhook-
            secrets
            (PR-D / ADR-012 §7 amendment). The CLI takes hex so the
            plaintext never has to be a binary argv value; the apid
            handler hex-decodes before the INSERT.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AdminSetGithubWebhookSecretResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
