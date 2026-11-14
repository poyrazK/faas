#!/usr/bin/env bash
# Record byte and trailer evidence, with identical Host/SNI through CF and direct.
# Usage: ... HTTP_HOST GRPC_HOST NEW_OUTPUT_DIR [capture|verify]
# capture records known failures; verify also requires bots/HTML/gRPC to pass.
set -euo pipefail
[[ $# -ge 3 && $# -le 4 ]] || { echo 'usage: cloudflare-zone-verify.sh HTTP_HOST GRPC_HOST NEW_OUTPUT_DIR [capture|verify]' >&2; exit 2; }
http=$1 grpc=$2 output=$3 mode=${4:-capture}
[[ $http =~ ^[a-z0-9-]+\.gregale\.dev$ && $grpc =~ ^[a-z0-9-]+\.gregale\.dev$ ]] || exit 2
[[ $mode == capture || $mode == verify ]] || exit 2
direct=${DIRECT_IP:-34.148.143.180}
python3 -c 'import ipaddress,sys; ipaddress.IPv4Address(sys.argv[1])' "$direct"
mkdir "$output" # Refuse to overwrite a previous capture.
script_dir=$(cd "$(dirname "$0")" && pwd)
date -u '+%Y-%m-%dT%H:%M:%SZ' >"$output/time.txt"
printf '\000\000\000\000\002hi' >"$output/frame.bin"
fetch() {
  local route=$1 name=$2 host=$3 path=$4
  shift 4
  # Keep the array nonempty: macOS Bash 3.2 treats an empty array as unset
  # under nounset even when it was explicitly initialized.
  local route_flags=(--noproxy '*')
  [[ $route != direct ]] || route_flags+=(--resolve "$host:443:$direct")
  curl -q --silent --show-error --http2 --proto '=https' \
    --connect-timeout 10 --max-time 40 "${route_flags[@]}" \
    --header 'Accept-Encoding: identity' --user-agent 'gregale-zone-probe/1.0' \
    --dump-header "$output/$route-$name.headers" --output "$output/$route-$name.body" \
    "$@" "https://$host$path"
}
for route in cloudflare direct; do
  for code in 200 500 502 503 504; do fetch "$route" "status-$code" "$http" "/status?code=$code"; done
  fetch "$route" html "$http" /html
  for entry in GPTBot ClaudeBot CCBot Bytespider OAI-SearchBot PerplexityBot Googlebot Claude-User \
    'GPTBot-versioned|GPTBot/1.2' 'ClaudeBot-versioned|ClaudeBot/1.0' \
    'CCBot-versioned|CCBot/2.0' 'Bytespider-versioned|Bytespider/1.0'; do
    ua=${entry#*|}; label=${entry%%|*}
    fetch "$route" "ua-$label" "$http" / --user-agent "$ua"
  done
  for method in Say Stream Fail; do
    fetch "$route" "grpc-$method" "$grpc" "/zone.Echo/$method" \
      --header 'content-type: application/grpc' --header 'te: trailers' \
      --data-binary "@$output/frame.bin"
    ip=-; [[ $route != direct ]] || ip=$direct
    node "$script_dir/cloudflare-zone-probes/h2-client.cjs" "$grpc" "$ip" "$method" \
      >"$output/$route-h2-$method.json" 2>"$output/$route-h2-$method.stderr" || true
  done
done
python3 - "$output" "$mode" "$http" "$grpc" "$direct" <<'PY'
import hashlib, json, pathlib, re, sys
p=pathlib.Path(sys.argv[1])
def read(route,name,ext='body'): return (p/f'{route}-{name}.{ext}').read_bytes()
def status(route,name):
    matches=re.findall(rb'^HTTP/[^ ]+ (\d+)', read(route,name,'headers'), re.M)
    return int(matches[-1]) if matches else None
def header(route,name,key):
    matches=re.findall(rb'^'+re.escape(key.encode())+rb': *([^\r\n]*)',read(route,name,'headers'),re.M|re.I)
    return matches[-1].decode() if matches else None
def equal(name): return read('cloudflare',name)==read('direct',name)
def probe(route,name):
    try: return json.loads(read(route,name)).get('probe')=='zone-byte-probe-v1'
    except (ValueError,AttributeError): return False
result={'http_host':sys.argv[3], 'grpc_host':sys.argv[4], 'direct_ip':sys.argv[5], 'statuses':{}, 'crawlers':{}, 'grpc':{}}
for code in (200,500,502,503,504):
    name=f'status-{code}'
    result['statuses'][str(code)]={'cloudflare':status('cloudflare',name), 'direct':status('direct',name),
        'body_equal':equal(name), 'direct_probe':probe('direct',name)}
agents={ua:ua for ua in ('GPTBot','ClaudeBot','CCBot','Bytespider','OAI-SearchBot','PerplexityBot','Googlebot','Claude-User')}
agents.update({'GPTBot-versioned':'GPTBot/1.2','ClaudeBot-versioned':'ClaudeBot/1.0','CCBot-versioned':'CCBot/2.0','Bytespider-versioned':'Bytespider/1.0'})
for label,ua in agents.items():
    name=f'ua-{label}'
    result['crawlers'][label]={'user_agent':ua, 'cloudflare':status('cloudflare',name), 'direct':status('direct',name),
        'body_equal':equal(name), 'cloudflare_probe':probe('cloudflare',name), 'direct_probe':probe('direct',name)}
result['html']={'body_equal':equal('html')}
for route in ('cloudflare','direct'):
    body=read(route,'html')
    result['html'][route]={'status':status(route,'html'), 'bytes':len(body), 'sha256':hashlib.sha256(body).hexdigest(),
        'jsd_injected':b'/cdn-cgi/challenge-platform/' in body, 'probe':b'zone-byte-probe-v1' in body,
        'csp':header(route,'html','content-security-policy')}
for method,expected in (('Say','0'),('Stream','0'),('Fail','9')):
    result['grpc'][method]={}
    for route in ('cloudflare','direct'):
        headers=read(route,f'grpc-{method}','headers').lower()
        statuses=re.findall(rb'^grpc-status: *([^\r\n]+)',headers,re.M)
        h2file=p/f'{route}-h2-{method}.json'
        try: h2=json.loads(h2file.read_text())
        except ValueError: h2={}
        h2status=(h2.get('trailers') or {}).get('grpc-status', (h2.get('headers') or {}).get('grpc-status'))
        result['grpc'][method][route]={'curl_grpc_status':[s.decode() for s in statuses],
            'h2_grpc_status':h2status, 'bytes':len(read(route,f'grpc-{method}')),
            'ok':status(route,f'grpc-{method}')==200 and statuses==[expected.encode()] and h2status==expected}
result['checks']={
    'direct_status_bodies':all(r['direct']==int(c) and r['direct_probe'] for c,r in result['statuses'].items()),
    'pass_through_200_500_503':all(result['statuses'][str(c)]['body_equal'] and result['statuses'][str(c)]['cloudflare']==c for c in (200,500,503)),
    'crawler_control':all(r['body_equal'] and r['cloudflare']==200 and r['direct']==200 and r['direct_probe'] and r['cloudflare_probe'] for r in result['crawlers'].values()),
    'html_unchanged':result['html']['body_equal'] and all(result['html'][r]['status']==200 and result['html'][r]['probe'] and not result['html'][r]['jsd_injected'] and result['html'][r]['csp']=="default-src 'none'" for r in ('cloudflare','direct')),
    'grpc':all(v[r]['ok'] for v in result['grpc'].values() for r in ('cloudflare','direct')),
}
(p/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
if sys.argv[2]=='verify' and not all(result['checks'].values()): sys.exit(1)
PY
