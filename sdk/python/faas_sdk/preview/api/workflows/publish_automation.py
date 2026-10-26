from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.automation_response import AutomationResponse
from ...models.problem import Problem
from ...models.publish_automation_request import PublishAutomationRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    name: str,
    *,
    body: PublishAutomationRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/automations/{name}/publish".format(
            slug=quote(str(slug), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AutomationResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AutomationResponse.from_dict(response.json())

        return response_200

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

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

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
) -> Response[AutomationResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishAutomationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AutomationResponse | Problem]:
    """Validate and publish the saved draft; taking over a YAML name requires explicit confirmation.

     Published definitions survive future YAML deployments. A live default deployment is required.
    Runtime execution also requires FAAS_WORKFLOWS_ENABLED on apid and schedd. Events accepted before
    publication retain their captured definition.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (PublishAutomationRequest): Saved draft revision to publish, with explicit YAML
            takeover when needed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishAutomationRequest,
    idempotency_key: str | Unset = UNSET,
) -> AutomationResponse | Problem | None:
    """Validate and publish the saved draft; taking over a YAML name requires explicit confirmation.

     Published definitions survive future YAML deployments. A live default deployment is required.
    Runtime execution also requires FAAS_WORKFLOWS_ENABLED on apid and schedd. Events accepted before
    publication retain their captured definition.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (PublishAutomationRequest): Saved draft revision to publish, with explicit YAML
            takeover when needed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        name=name,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishAutomationRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[AutomationResponse | Problem]:
    """Validate and publish the saved draft; taking over a YAML name requires explicit confirmation.

     Published definitions survive future YAML deployments. A live default deployment is required.
    Runtime execution also requires FAAS_WORKFLOWS_ENABLED on apid and schedd. Events accepted before
    publication retain their captured definition.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (PublishAutomationRequest): Saved draft revision to publish, with explicit YAML
            takeover when needed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AutomationResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        name=name,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
    body: PublishAutomationRequest,
    idempotency_key: str | Unset = UNSET,
) -> AutomationResponse | Problem | None:
    """Validate and publish the saved draft; taking over a YAML name requires explicit confirmation.

     Published definitions survive future YAML deployments. A live default deployment is required.
    Runtime execution also requires FAAS_WORKFLOWS_ENABLED on apid and schedd. Events accepted before
    publication retain their captured definition.

    Args:
        slug (str):
        name (str):
        idempotency_key (str | Unset):
        body (PublishAutomationRequest): Saved draft revision to publish, with explicit YAML
            takeover when needed.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AutomationResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            name=name,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
