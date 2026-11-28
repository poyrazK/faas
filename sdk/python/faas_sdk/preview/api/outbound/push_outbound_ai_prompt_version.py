from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.push_outbound_ai_prompt_version_request import PushOutboundAIPromptVersionRequest
from ...models.push_outbound_ai_prompt_version_response import PushOutboundAIPromptVersionResponse
from ...types import Response


def _get_kwargs(
    prompt: str,
    *,
    body: PushOutboundAIPromptVersionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/outbound/ai-prompts/{prompt}/versions".format(
            prompt=quote(str(prompt), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | PushOutboundAIPromptVersionResponse | None:
    if response.status_code == 201:
        response_201 = PushOutboundAIPromptVersionResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | PushOutboundAIPromptVersionResponse]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PushOutboundAIPromptVersionRequest,
) -> Response[Problem | PushOutboundAIPromptVersionResponse]:
    """Push a managed AI prompt version.

     Requires MFA and deploy-write scope. Stores an immutable version, creating the prompt on its first
    version, which becomes live. A later version is live only when live is true. A push identical to the
    latest version stores nothing and answers 200 with created false. {{name}} placeholders in message
    content are replaced by the call's variables or the version's defaults as plain text. The target is
    an openai-format route, or a managed AI integration an openai-format route accepts, with a model.
    Each prompt keeps 200 versions; prompts per account follow the plan.

    Args:
        prompt (str):
        body (PushOutboundAIPromptVersionRequest): A new version. messages, defaults and params
            together are at most 64 KiB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PushOutboundAIPromptVersionResponse]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PushOutboundAIPromptVersionRequest,
) -> Problem | PushOutboundAIPromptVersionResponse | None:
    """Push a managed AI prompt version.

     Requires MFA and deploy-write scope. Stores an immutable version, creating the prompt on its first
    version, which becomes live. A later version is live only when live is true. A push identical to the
    latest version stores nothing and answers 200 with created false. {{name}} placeholders in message
    content are replaced by the call's variables or the version's defaults as plain text. The target is
    an openai-format route, or a managed AI integration an openai-format route accepts, with a model.
    Each prompt keeps 200 versions; prompts per account follow the plan.

    Args:
        prompt (str):
        body (PushOutboundAIPromptVersionRequest): A new version. messages, defaults and params
            together are at most 64 KiB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PushOutboundAIPromptVersionResponse
    """

    return sync_detailed(
        prompt=prompt,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PushOutboundAIPromptVersionRequest,
) -> Response[Problem | PushOutboundAIPromptVersionResponse]:
    """Push a managed AI prompt version.

     Requires MFA and deploy-write scope. Stores an immutable version, creating the prompt on its first
    version, which becomes live. A later version is live only when live is true. A push identical to the
    latest version stores nothing and answers 200 with created false. {{name}} placeholders in message
    content are replaced by the call's variables or the version's defaults as plain text. The target is
    an openai-format route, or a managed AI integration an openai-format route accepts, with a model.
    Each prompt keeps 200 versions; prompts per account follow the plan.

    Args:
        prompt (str):
        body (PushOutboundAIPromptVersionRequest): A new version. messages, defaults and params
            together are at most 64 KiB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | PushOutboundAIPromptVersionResponse]
    """

    kwargs = _get_kwargs(
        prompt=prompt,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    prompt: str,
    *,
    client: AuthenticatedClient | Client,
    body: PushOutboundAIPromptVersionRequest,
) -> Problem | PushOutboundAIPromptVersionResponse | None:
    """Push a managed AI prompt version.

     Requires MFA and deploy-write scope. Stores an immutable version, creating the prompt on its first
    version, which becomes live. A later version is live only when live is true. A push identical to the
    latest version stores nothing and answers 200 with created false. {{name}} placeholders in message
    content are replaced by the call's variables or the version's defaults as plain text. The target is
    an openai-format route, or a managed AI integration an openai-format route accepts, with a model.
    Each prompt keeps 200 versions; prompts per account follow the plan.

    Args:
        prompt (str):
        body (PushOutboundAIPromptVersionRequest): A new version. messages, defaults and params
            together are at most 64 KiB.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | PushOutboundAIPromptVersionResponse
    """

    return (
        await asyncio_detailed(
            prompt=prompt,
            client=client,
            body=body,
        )
    ).parsed
