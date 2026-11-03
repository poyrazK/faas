from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_outbound_ai_access_token_request import CreateOutboundAIAccessTokenRequest
from ...models.created_outbound_ai_access_token import CreatedOutboundAIAccessToken
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateOutboundAIAccessTokenRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/ai-access-tokens".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CreatedOutboundAIAccessToken | Problem | None:
    if response.status_code == 201:
        response_201 = CreatedOutboundAIAccessToken.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CreatedOutboundAIAccessToken | Problem]:
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
    body: CreateOutboundAIAccessTokenRequest,
) -> Response[CreatedOutboundAIAccessToken | Problem]:
    """Issue an AI gateway access token for an app.

     Requires MFA and deploy-write scope. A caller outside Gregale, such as local development, CI or a
    notebook, sets its SDK's base URL to the AI gateway's public host and puts the token where the SDK
    puts its API key (Authorization Bearer, x-api-key, x-goog-api-key or api-key). Its calls are the
    app's: the app's bindings, guardrails, caps and content policy apply. The secret is returned only in
    this response. An app holds at most 20 live tokens.

    Args:
        slug (str):
        body (CreateOutboundAIAccessTokenRequest): Issue an access token for an app.
            expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to listed AI
            integrations bound to the app; empty allows every AI integration bound to the app and the
            account's routes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreatedOutboundAIAccessToken | Problem]
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
    body: CreateOutboundAIAccessTokenRequest,
) -> CreatedOutboundAIAccessToken | Problem | None:
    """Issue an AI gateway access token for an app.

     Requires MFA and deploy-write scope. A caller outside Gregale, such as local development, CI or a
    notebook, sets its SDK's base URL to the AI gateway's public host and puts the token where the SDK
    puts its API key (Authorization Bearer, x-api-key, x-goog-api-key or api-key). Its calls are the
    app's: the app's bindings, guardrails, caps and content policy apply. The secret is returned only in
    this response. An app holds at most 20 live tokens.

    Args:
        slug (str):
        body (CreateOutboundAIAccessTokenRequest): Issue an access token for an app.
            expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to listed AI
            integrations bound to the app; empty allows every AI integration bound to the app and the
            account's routes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreatedOutboundAIAccessToken | Problem
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
    body: CreateOutboundAIAccessTokenRequest,
) -> Response[CreatedOutboundAIAccessToken | Problem]:
    """Issue an AI gateway access token for an app.

     Requires MFA and deploy-write scope. A caller outside Gregale, such as local development, CI or a
    notebook, sets its SDK's base URL to the AI gateway's public host and puts the token where the SDK
    puts its API key (Authorization Bearer, x-api-key, x-goog-api-key or api-key). Its calls are the
    app's: the app's bindings, guardrails, caps and content policy apply. The secret is returned only in
    this response. An app holds at most 20 live tokens.

    Args:
        slug (str):
        body (CreateOutboundAIAccessTokenRequest): Issue an access token for an app.
            expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to listed AI
            integrations bound to the app; empty allows every AI integration bound to the app and the
            account's routes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CreatedOutboundAIAccessToken | Problem]
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
    body: CreateOutboundAIAccessTokenRequest,
) -> CreatedOutboundAIAccessToken | Problem | None:
    """Issue an AI gateway access token for an app.

     Requires MFA and deploy-write scope. A caller outside Gregale, such as local development, CI or a
    notebook, sets its SDK's base URL to the AI gateway's public host and puts the token where the SDK
    puts its API key (Authorization Bearer, x-api-key, x-goog-api-key or api-key). Its calls are the
    app's: the app's bindings, guardrails, caps and content policy apply. The secret is returned only in
    this response. An app holds at most 20 live tokens.

    Args:
        slug (str):
        body (CreateOutboundAIAccessTokenRequest): Issue an access token for an app.
            expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to listed AI
            integrations bound to the app; empty allows every AI integration bound to the app and the
            account's routes.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CreatedOutboundAIAccessToken | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
