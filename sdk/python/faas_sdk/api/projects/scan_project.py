from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.plan_response import PlanResponse
from ...models.problem import Problem
from ...models.project_scan_request import ProjectScanRequest
from ...types import Response


def _get_kwargs(
    *,
    body: ProjectScanRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/projects/scan",
    }

    _kwargs["files"] = body.to_multipart()

    headers["Content-Type"] = "multipart/form-data; boundary=+++"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> PlanResponse | Problem | None:
    if response.status_code == 200:
        response_200 = PlanResponse.from_dict(response.json())

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
) -> Response[PlanResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectScanRequest,
) -> Response[PlanResponse | Problem]:
    """Scan an uploaded tarball and return a deploy plan.

     Dry-run. Accepts a multipart upload (`source=<tar.gz>`,
    `project_slug`, `production_branch`, `install_id`, `only`)
    and returns a PlanResponse with the discovered workloads,
    managed services, derived scan_source, and a plan_token
    that the apply endpoint can echo back to skip the
    second extract.

    On over-quota the response carries `can_apply=false`
    (and `crons_not_allowed=true` for Free plan) so the CLI
    can branch without a second request.

    Args:
        body (ProjectScanRequest): Multipart body for POST /v1/projects/scan (dry-run).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlanResponse | Problem]
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
    body: ProjectScanRequest,
) -> PlanResponse | Problem | None:
    """Scan an uploaded tarball and return a deploy plan.

     Dry-run. Accepts a multipart upload (`source=<tar.gz>`,
    `project_slug`, `production_branch`, `install_id`, `only`)
    and returns a PlanResponse with the discovered workloads,
    managed services, derived scan_source, and a plan_token
    that the apply endpoint can echo back to skip the
    second extract.

    On over-quota the response carries `can_apply=false`
    (and `crons_not_allowed=true` for Free plan) so the CLI
    can branch without a second request.

    Args:
        body (ProjectScanRequest): Multipart body for POST /v1/projects/scan (dry-run).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlanResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectScanRequest,
) -> Response[PlanResponse | Problem]:
    """Scan an uploaded tarball and return a deploy plan.

     Dry-run. Accepts a multipart upload (`source=<tar.gz>`,
    `project_slug`, `production_branch`, `install_id`, `only`)
    and returns a PlanResponse with the discovered workloads,
    managed services, derived scan_source, and a plan_token
    that the apply endpoint can echo back to skip the
    second extract.

    On over-quota the response carries `can_apply=false`
    (and `crons_not_allowed=true` for Free plan) so the CLI
    can branch without a second request.

    Args:
        body (ProjectScanRequest): Multipart body for POST /v1/projects/scan (dry-run).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[PlanResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ProjectScanRequest,
) -> PlanResponse | Problem | None:
    """Scan an uploaded tarball and return a deploy plan.

     Dry-run. Accepts a multipart upload (`source=<tar.gz>`,
    `project_slug`, `production_branch`, `install_id`, `only`)
    and returns a PlanResponse with the discovered workloads,
    managed services, derived scan_source, and a plan_token
    that the apply endpoint can echo back to skip the
    second extract.

    On over-quota the response carries `can_apply=false`
    (and `crons_not_allowed=true` for Free plan) so the CLI
    can branch without a second request.

    Args:
        body (ProjectScanRequest): Multipart body for POST /v1/projects/scan (dry-run).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        PlanResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
