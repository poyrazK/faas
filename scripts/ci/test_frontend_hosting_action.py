#!/usr/bin/env python3
"""Hermetic Action contracts: no network, real credentials, or GitHub writes."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import uuid

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('frontend_action',ROOT/'.github/actions/frontend-hosting/run.py')
action=importlib.util.module_from_spec(spec);spec.loader.exec_module(action)
SITE='11111111-1111-4111-8111-111111111111'
RELEASE='22222222-2222-4222-8222-222222222222'
PREVIEW='33333333-3333-4333-8333-333333333333'
TOKEN='fp_host_'+'a'*64
class ActionContracts(unittest.TestCase):
 def invoke(self,extra=None,result=None,returncode=0):
  import io
  from contextlib import redirect_stdout,redirect_stderr
  from types import SimpleNamespace
  with tempfile.TemporaryDirectory() as tmp:
   output=Path(tmp)/'output';env={'PATH':os.environ['PATH'],'HOSTING_ENABLED':'true','FAAS_TOKEN':TOKEN,'FAAS_API':'https://api.example.com','HOSTING_SITE':SITE,'HOSTING_CLI':'gregale','GITHUB_REPOSITORY':'org/site','GITHUB_RUN_ID':'12345','GITHUB_SHA':'b'*40,'GITHUB_REF_NAME':'feature/branch','HOSTING_DESCRIPTION':'$(touch /tmp/not-executed); echo unsafe','GITHUB_OUTPUT':str(output)}
   env.update(extra or {})
   expected=str(uuid.uuid5(uuid.NAMESPACE_URL,'gregale-hosting:https://api.example.com/'+SITE+':org/site:12345'))
   data=result if result is not None else {'run':{'id':expected,'site_id':SITE,'release_id':RELEASE,'preview_id':PREVIEW},'state':'publication_acknowledged','published':True,'preview_url':'https://p-'+PREVIEW+'.hosting.example'}
   stdout,stderr=io.StringIO(),io.StringIO()
   with patch.dict(os.environ,env,clear=True),patch.object(action.shutil,'which',return_value='/trusted/gregale'),patch.object(action.subprocess,'run',return_value=SimpleNamespace(returncode=returncode,stdout=json.dumps(data),stderr=TOKEN+' signed-private-url')) as run,redirect_stdout(stdout),redirect_stderr(stderr):
    code=action.main()
   return code,stdout.getvalue()+stderr.getvalue(),output.read_text() if output.exists() else '',run.call_args,run.call_count
 def test_rerun_identity_injection_and_no_secret_logging(self):
  first=self.invoke();second=self.invoke({'GITHUB_RUN_ATTEMPT':'2'})
  self.assertEqual(first[0],0);self.assertEqual(first[2],second[2]);self.assertNotIn(TOKEN,first[1]+first[2])
  args=first[3].args[0];self.assertEqual(args[1:3],['sites','ci']);self.assertNotIn(TOKEN,str(args));self.assertIn('$(touch /tmp/not-executed); echo unsafe',args)
  self.assertEqual(first[3].kwargs['env']['FAAS_TOKEN'],TOKEN);self.assertNotIn('shell',first[3].kwargs)
 def test_preview_reservations_are_explicit_and_retry_stable(self):
  plain=self.invoke();reserved=self.invoke({'HOSTING_RESERVE_PREVIEW':'true'})
  self.assertNotIn('--reserve-preview',plain[3].args[0])
  self.assertIn('--reserve-preview',reserved[3].args[0])
  self.assertEqual(reserved[3].kwargs['env']['FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED'],'1')
  self.assertEqual(reserved[2],self.invoke({'HOSTING_RESERVE_PREVIEW':'true','GITHUB_RUN_ATTEMPT':'2'})[2])
  self.assertEqual(self.invoke({'HOSTING_RESERVE_PREVIEW':'TRUE'})[4],0)
 def test_canceled_intent_never_suggests_retry(self):
  run=str(uuid.uuid5(uuid.NAMESPACE_URL,'gregale-hosting:https://api.example.com/'+SITE+':org/site:12345'))
  code,text,output,args,calls=self.invoke(result={'run':{'id':run,'site_id':SITE,'release_id':RELEASE,'preview_id':PREVIEW},'state':'canceled','published':False},returncode=1)
  self.assertEqual(code,1);self.assertIn('permanently canceled',text);self.assertNotIn('Retry unchanged',text);self.assertIn('publication-state=canceled',output)
 def test_default_off_and_generic_credentials_never_execute_cli(self):
  for overrides in [{'HOSTING_ENABLED':'false'},{'FAAS_TOKEN':'fp_live_'+'a'*48},{'FAAS_API':'http://api.example.com'},{'FAAS_API':'https://user:password@api.example.com'},{'FAAS_API':'https://api.example.com/path'},{'HOSTING_RUN_ID':'bad'}]:
   code,text,output,args,calls=self.invoke(overrides);self.assertEqual(code,1);self.assertEqual(calls,0);self.assertEqual(output,'');self.assertNotIn(TOKEN,text)
 def test_failure_retains_safe_identifiers_without_cli_error_text(self):
  run=str(uuid.uuid5(uuid.NAMESPACE_URL,'gregale-hosting:https://api.example.com/'+SITE+':org/site:12345'))
  code,text,output,args,calls=self.invoke(result={'run':{'id':run,'site_id':SITE,'release_id':RELEASE,'preview_id':PREVIEW},'state':'publication_pending','published':False},returncode=1)
  self.assertEqual(code,1);self.assertIn('run-id='+run,output);self.assertIn('release-id='+RELEASE,output);self.assertNotIn(TOKEN,text+output);self.assertNotIn('signed-private-url',text)
 def test_success_requires_matching_identity_and_publication_receipt(self):
  for result in [{},[],{'run':{'id':RELEASE,'site_id':SITE}},{'run':{'id':str(uuid.uuid5(uuid.NAMESPACE_URL,'gregale-hosting:https://api.example.com/'+SITE+':org/site:12345')),'site_id':SITE},'state':'publication_pending','published':False}]:
   self.assertEqual(self.invoke(result=result)[0],1)
 def test_oidc_is_explicit_and_preserves_identity_without_token_arguments(self):
  overrides={'FAAS_TOKEN':'','HOSTING_OIDC_ENABLED':'true','HOSTING_OIDC_TRUST':RELEASE,'ACTIONS_ID_TOKEN_REQUEST_URL':'https://runner.actions.githubusercontent.com/job','ACTIONS_ID_TOKEN_REQUEST_TOKEN':'github-secret'}
  code,text,output,args,calls=self.invoke(overrides)
  self.assertEqual(code,0);self.assertIn('--oidc-trust',args.args[0]);self.assertIn(RELEASE,args.args[0]);self.assertNotIn('github-secret',str(args.args[0])+text+output)
  self.assertEqual(args.kwargs['env']['FAAS_FRONTEND_HOSTING_OIDC_ENABLED'],'1');self.assertEqual(args.kwargs['env']['FAAS_TOKEN'],'')
  self.assertEqual(output,self.invoke({**overrides,'GITHUB_RUN_ATTEMPT':'2'})[2])
  for invalid in [{'HOSTING_OIDC_ENABLED':'false'},{'FAAS_TOKEN':TOKEN},{'HOSTING_OIDC_TRUST':'bad'},{'HOSTING_OIDC_ENABLED':'TRUE'}]:
   self.assertEqual(self.invoke({**overrides,**invalid})[4],0)
 def test_malformed_private_url_is_rejected_without_echoing_it(self):
  run=str(uuid.uuid5(uuid.NAMESPACE_URL,'gregale-hosting:https://api.example.com/'+SITE+':org/site:12345'))
  for value in ['https://[invalid', {'secret':TOKEN}, 'https://user:'+TOKEN+'@hosting.example', 'https://p-'+PREVIEW+'.hosting.example/\\ninjected-output=yes']:
   result={'run':{'id':run,'site_id':SITE,'release_id':RELEASE,'preview_id':PREVIEW},'state':'publication_acknowledged','published':True,'preview_url':value}
   code,text,output,args,calls=self.invoke(result=result)
   self.assertEqual(code,1);self.assertNotIn(TOKEN,text+output);self.assertEqual(output,'')
if __name__=='__main__':unittest.main()
