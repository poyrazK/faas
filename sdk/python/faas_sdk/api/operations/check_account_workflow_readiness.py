from http import HTTPStatus
from typing import Any
from urllib.parse import quote
import httpx
from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.operation_workflow_readiness_request import OperationWorkflowReadinessRequest
from ...models.operation_workflow_readiness_response import OperationWorkflowReadinessResponse
from ...models.problem import Problem
from ...types import Response

def _get_kwargs(slug: str, *,body:OperationWorkflowReadinessRequest)->dict[str,Any]:
    return {"method":"post","url":"/v1/apps/"+quote(slug,safe="")+"/workflow-readiness","json":body.to_dict(),"headers":{"Content-Type":"application/json"}}

def _parse_response(*,client:AuthenticatedClient|Client,response:httpx.Response)->OperationWorkflowReadinessResponse|Problem|None:
    if response.status_code==200: return OperationWorkflowReadinessResponse.from_dict(response.json())
    if response.status_code in (400,401,403,404,409,429,503): return Problem.from_dict(response.json())
    if client.raise_on_unexpected_status: raise errors.UnexpectedStatus(response.status_code,response.content)
    return None

def _build_response(*,client:AuthenticatedClient|Client,response:httpx.Response)->Response[OperationWorkflowReadinessResponse|Problem]:
    return Response(status_code=HTTPStatus(response.status_code),content=response.content,headers=response.headers,parsed=_parse_response(client=client,response=response))

def sync_detailed(slug: str, *,client:AuthenticatedClient|Client,body:OperationWorkflowReadinessRequest)->Response[OperationWorkflowReadinessResponse|Problem]:
    response=client.get_httpx_client().request(**_get_kwargs(slug, body=body))
    return _build_response(client=client,response=response)

def sync(slug: str, *,client:AuthenticatedClient|Client,body:OperationWorkflowReadinessRequest)->OperationWorkflowReadinessResponse|Problem|None:
    return sync_detailed(slug, client=client,body=body).parsed

async def asyncio_detailed(slug: str, *,client:AuthenticatedClient|Client,body:OperationWorkflowReadinessRequest)->Response[OperationWorkflowReadinessResponse|Problem]:
    response=await client.get_async_httpx_client().request(**_get_kwargs(slug, body=body))
    return _build_response(client=client,response=response)

async def asyncio(slug: str, *,client:AuthenticatedClient|Client,body:OperationWorkflowReadinessRequest)->OperationWorkflowReadinessResponse|Problem|None:
    return (await asyncio_detailed(slug, client=client,body=body)).parsed
