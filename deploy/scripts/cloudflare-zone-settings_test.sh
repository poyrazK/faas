#!/usr/bin/env bash
# Real TTY input and a fake curl: no live credential or network is used.
set -euo pipefail
python3 - "$(cd "$(dirname "$0")" && pwd)/cloudflare-zone-settings.sh" <<'PY'
import errno, json, os, pathlib, pty, select, subprocess, sys, tempfile, time

script = sys.argv[1]
secret = 'TEST-ONLY-NOT-A-REAL-TOKEN'
zone = 'c0baeb8d33b48ec1704766edcb695394'
with tempfile.TemporaryDirectory() as directory:
    root = pathlib.Path(directory)
    (root / 'bin').mkdir()
    curl = root / 'bin/curl'
    curl.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
a=sys.argv[1:]
assert all('TEST-ONLY' not in v for v in a), 'secret in argv'
assert all('TEST-ONLY' not in v for v in os.environ.values()), 'secret in environment'
assert sys.stdin.read() == 'Authorization: Bearer TEST-ONLY-NOT-A-REAL-TOKEN\\n'
assert a[0] == '-q' and '--location' not in a and '-L' not in a
assert a[a.index('--noproxy')+1] == '*'
assert a[a.index('--header')+1] == '@-'
root=pathlib.Path(os.environ['MOCK_DIR'])
state=json.loads((root/'state').read_text())
method=a[a.index('--request')+1]
url=next(v for v in a if v.startswith('https://'))
prefix='https://api.cloudflare.com/client/v4/zones/c0baeb8d33b48ec1704766edcb695394'
assert url.startswith(prefix)
path=url[len(prefix):]
data=json.loads(a[a.index('--data')+1]) if '--data' in a else None
with (root/'calls').open('a') as f: f.write(json.dumps([method,path,data])+'\\n')
if state.get('failure') == 'http': sys.exit(22)
if state.get('failure') == 'api': print('{"success":false,"result":{}}'); sys.exit()
key={'':'zone','/bot_management':'bots','/settings/grpc':'grpc'}[path]
if method != 'GET':
 assert (method,path) in [('PUT','/bot_management'),('PATCH','/settings/grpc')]
 assert set(data) == ({'fight_mode','enable_js','ai_bots_protection'} if key=='bots' else {'value'})
 if state.get('failure') != 'readback': state[key].update(data)
 (root/'state').write_text(json.dumps(state))
print(json.dumps({'success':True,'result':state[key]}))
''')
    curl.chmod(0o755)
    def state():
        return {'zone': {'id':zone, 'name':'gregale.dev', 'status':'active', 'paused':False, 'plan':{'legacy_id':'free'}},
                'bots': {'fight_mode':True, 'enable_js':True, 'ai_bots_protection':'block', 'ai_search':'disabled', 'crawler_protection':'disabled'},
                'grpc': {'id':'grpc', 'value':'off', 'editable':True}}
    def run(action='plan', apply='0', initial=None, extra=None):
        initial = initial or state()
        (root/'state').write_text(json.dumps(initial))
        (root/'calls').write_text('')
        env=dict(os.environ, PATH=str(root/'bin')+':'+os.environ['PATH'], MOCK_DIR=str(root), APPLY=apply)
        env.pop('CF_API_TOKEN',None)
        env.pop('CF_ZONE_ID',None)
        env.update(extra or {})
        pid, master=pty.fork()
        if pid==0: os.execvpe('bash', ['bash','-x',script,action], env)
        output=b''; sent=False; end=time.monotonic()+10
        try:
            while True:
                assert time.monotonic()<end, 'script timeout'
                if select.select([master],[],[],.1)[0]:
                    try: chunk=os.read(master,65536)
                    except OSError as e:
                        if e.errno==errno.EIO: break
                        raise
                    if not chunk: break
                    output+=chunk
                    if b'Cloudflare token (' in output and not sent:
                        time.sleep(.05)
                        os.write(master,(secret+'\n').encode()); sent=True
            _, status=os.waitpid(pid,0)
        finally: os.close(master)
        text=output.decode()
        assert secret not in text, 'secret printed'
        calls=[json.loads(line) for line in (root/'calls').read_text().splitlines()]
        return os.waitstatus_to_exitcode(status), text, calls, json.loads((root/'state').read_text())
    def writes(calls): return [c for c in calls if c[0]!='GET']
    for action in ('plan','bots','grpc'):
        rc,out,calls,_=run(action)
        assert rc==0 and not writes(calls), out
    rc,out,calls,after=run('bots','1')
    assert rc==0 and len(writes(calls))==1, out
    assert after['bots']['fight_mode'] is False and after['bots']['enable_js'] is False
    assert after['bots']['ai_bots_protection']=='disabled' and after['bots']['ai_search']=='disabled'
    assert after['bots']['crawler_protection']=='disabled' and after['grpc']['value']=='off'
    rc,out,calls,_=run('bots','1',after)
    assert rc==0 and not writes(calls), out
    initial=state(); initial['bots'].pop('enable_js')
    assert run('bots','1',initial)[0]==0
    rc,out,calls,after=run('grpc','1')
    assert rc==0 and writes(calls)==[['PATCH','/settings/grpc',{'value':'on'}]], out
    assert after['bots']==state()['bots']
    assert not writes(run('grpc','1',after)[2])
    for mutate in (
        lambda d:d['zone'].update(name='other.dev'),
        lambda d:d['zone'].update(paused=True),
        lambda d:d['zone']['plan'].update(legacy_id='enterprise'),
        lambda d:d['bots'].pop('fight_mode'),
        lambda d:d['bots'].update(ai_bots_protection='new-unknown-value'),
        lambda d:d['grpc'].update(value='unknown'),
    ):
        initial=state(); mutate(initial)
        rc,out,calls,_=run('bots','1',initial)
        assert rc!=0 and not writes(calls), out
    for failure in ('api','http','readback'):
        initial=state(); initial['failure']=failure
        rc,out,calls,_=run('bots','1',initial)
        assert rc!=0 and len(writes(calls))==(1 if failure=='readback' else 0), out
    for extra in ({'CF_API_TOKEN':'do-not-use-env'}, {'CF_ZONE_ID':'other-zone'}, {'APPLY':'yes'}):
        rc,out,calls,_=run('bots','1',extra=extra)
        assert rc!=0 and not calls, out
    assert run(extra={'token':secret})[0]==0 # inherited export is removed
    assert run('unknown')[0]!=0
    no_tty=subprocess.run(['bash',script],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    assert no_tty.returncode!=0
print('cloudflare-zone-settings_test: OK (dry run, TTY secrecy, scope, idempotence, errors, readback)')
PY
