from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.anonymous_session_request import AnonymousSessionRequest
from ...models.anonymous_session_response import AnonymousSessionResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AnonymousSessionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/anonymous/sessions",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AnonymousSessionResponse | Problem | None:
    if response.status_code == 201:
        response_201 = AnonymousSessionResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

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
) -> Response[AnonymousSessionResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AnonymousSessionRequest,
) -> Response[AnonymousSessionResponse | Problem]:
    """Deploy without an account; a person claims the app later.

     For agents working for someone without a Gregale account. Needs no
    credential. Creates a Free account limited to deploying and reading
    its apps, and returns its deploy key and a claim link. A person who
    opens the link and confirms an email address owns the account and
    its apps; the key then stops working. Unclaimed accounts are deleted
    after the claim window (72 hours by default). Custom domains,
    databases, buckets, cron, API keys, organizations and billing answer
    `claim_required` until the claim. New sessions are limited per
    network and across the platform (`anonymous_deploys_unavailable`).
    Answers 404 while the feature is not offered.

    Args:
        body (AnonymousSessionRequest): Body for `POST /v1/anonymous/sessions`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AnonymousSessionResponse | Problem]
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
    body: AnonymousSessionRequest,
) -> AnonymousSessionResponse | Problem | None:
    """Deploy without an account; a person claims the app later.

     For agents working for someone without a Gregale account. Needs no
    credential. Creates a Free account limited to deploying and reading
    its apps, and returns its deploy key and a claim link. A person who
    opens the link and confirms an email address owns the account and
    its apps; the key then stops working. Unclaimed accounts are deleted
    after the claim window (72 hours by default). Custom domains,
    databases, buckets, cron, API keys, organizations and billing answer
    `claim_required` until the claim. New sessions are limited per
    network and across the platform (`anonymous_deploys_unavailable`).
    Answers 404 while the feature is not offered.

    Args:
        body (AnonymousSessionRequest): Body for `POST /v1/anonymous/sessions`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AnonymousSessionResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AnonymousSessionRequest,
) -> Response[AnonymousSessionResponse | Problem]:
    """Deploy without an account; a person claims the app later.

     For agents working for someone without a Gregale account. Needs no
    credential. Creates a Free account limited to deploying and reading
    its apps, and returns its deploy key and a claim link. A person who
    opens the link and confirms an email address owns the account and
    its apps; the key then stops working. Unclaimed accounts are deleted
    after the claim window (72 hours by default). Custom domains,
    databases, buckets, cron, API keys, organizations and billing answer
    `claim_required` until the claim. New sessions are limited per
    network and across the platform (`anonymous_deploys_unavailable`).
    Answers 404 while the feature is not offered.

    Args:
        body (AnonymousSessionRequest): Body for `POST /v1/anonymous/sessions`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AnonymousSessionResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AnonymousSessionRequest,
) -> AnonymousSessionResponse | Problem | None:
    """Deploy without an account; a person claims the app later.

     For agents working for someone without a Gregale account. Needs no
    credential. Creates a Free account limited to deploying and reading
    its apps, and returns its deploy key and a claim link. A person who
    opens the link and confirms an email address owns the account and
    its apps; the key then stops working. Unclaimed accounts are deleted
    after the claim window (72 hours by default). Custom domains,
    databases, buckets, cron, API keys, organizations and billing answer
    `claim_required` until the claim. New sessions are limited per
    network and across the platform (`anonymous_deploys_unavailable`).
    Answers 404 while the feature is not offered.

    Args:
        body (AnonymousSessionRequest): Body for `POST /v1/anonymous/sessions`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AnonymousSessionResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
