from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.apply_response import ApplyResponse
from ...models.problem import Problem
from ...models.project_apply_request import ProjectApplyRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    organization: str,
    *,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    params: dict[str, Any] = {}

    params["plan_token"] = plan_token

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{organization}/projects".format(
            organization=quote(str(organization), safe=""),
        ),
        "params": params,
    }

    _kwargs["files"] = body.to_multipart()

    headers["Content-Type"] = "multipart/form-data; boundary=+++"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApplyResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ApplyResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApplyResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Create or apply an organization project from uploaded source.

     Upload source and project_slug using the existing multipart contract. An adopted organization
    supplies the new project resource namespace and deployment budget. Database and async-route manifest
    bindings are rejected before personal services are resolved.

    Args:
        organization (str):
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Create or apply an organization project from uploaded source.

     Upload source and project_slug using the existing multipart contract. An adopted organization
    supplies the new project resource namespace and deployment budget. Database and async-route manifest
    bindings are rejected before personal services are resolved.

    Args:
        organization (str):
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return sync_detailed(
        organization=organization,
        client=client,
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> Response[ApplyResponse | Problem]:
    """Create or apply an organization project from uploaded source.

     Upload source and project_slug using the existing multipart contract. An adopted organization
    supplies the new project resource namespace and deployment budget. Database and async-route manifest
    bindings are rejected before personal services are resolved.

    Args:
        organization (str):
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApplyResponse | Problem]
    """

    kwargs = _get_kwargs(
        organization=organization,
        body=body,
        plan_token=plan_token,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    organization: str,
    *,
    client: AuthenticatedClient | Client,
    body: ProjectApplyRequest,
    plan_token: str | Unset = UNSET,
    idempotency_key: str | Unset = UNSET,
) -> ApplyResponse | Problem | None:
    """Create or apply an organization project from uploaded source.

     Upload source and project_slug using the existing multipart contract. An adopted organization
    supplies the new project resource namespace and deployment budget. Database and async-route manifest
    bindings are rejected before personal services are resolved.

    Args:
        organization (str):
        plan_token (str | Unset):
        idempotency_key (str | Unset):
        body (ProjectApplyRequest): Multipart body for POST /v1/projects (apply). Same shape as
            ProjectScanRequest; the apply handler resolves AppIDs and
            inserts crons in a follow-up pass.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApplyResponse | Problem
    """

    return (
        await asyncio_detailed(
            organization=organization,
            client=client,
            body=body,
            plan_token=plan_token,
            idempotency_key=idempotency_key,
        )
    ).parsed
