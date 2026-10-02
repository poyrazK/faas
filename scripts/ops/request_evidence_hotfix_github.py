"""Read-only GitHub access without installing tools or forwarding tokens to storage."""
import io
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import urllib.error
import urllib.request
import urllib.parse
import zipfile

REPO = 'poyrazK/faas'


def request(path):
    assert not path.startswith('/') and '..' not in path and '://' not in path
    return urllib.request.Request('https://api.github.com/repos/' + REPO + '/' + path,
        headers={'Authorization': 'Bearer ' + os.environ['GH_TOKEN'],
                 'Accept': 'application/vnd.github+json', 'User-Agent': 'Gregale-Component-Hotfix'})


def api(path):
    with urllib.request.urlopen(request(path), timeout=60) as response:
        return json.load(response)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def artifact_bytes(artifact_id):
    # Obtain GitHub's signed storage URL with the token, then discard the
    # authenticated Request. The storage download receives no Authorization.
    opener = urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(request('actions/artifacts/' + str(artifact_id) + '/zip'), timeout=60) as response:
            data = response.read(50_000_001)
    except urllib.error.HTTPError as error:
        assert error.code == 302
        location = error.headers['Location']
        assert urllib.parse.urlsplit(location).scheme == 'https'
        with urllib.request.urlopen(location, timeout=60) as response:
            data = response.read(50_000_001)
    assert len(data) <= 50_000_000
    return data


def extract_metadata(data, root):
    root = Path(root)
    root.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        members = archive.infolist()
        assert len(members) <= 512 and sum(x.file_size for x in members) <= 50_000_000
        for member in members:
            path = root / member.filename
            assert not Path(member.filename).is_absolute()
            path.resolve().relative_to(root.resolve())
            assert not stat.S_ISLNK(member.external_attr >> 16)
            if member.is_dir():
                path.mkdir(parents=True, exist_ok=True)
            else:
                assert path.suffix in ['.json', '.log']
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(archive.read(member))


def download_artifact(run_id, name, root):
    rows = api('actions/runs/' + str(run_id) + '/artifacts?per_page=100')['artifacts']
    matches = [x for x in rows if x['name'] == name and not x['expired']]
    assert len(matches) == 1
    data = artifact_bytes(matches[0]['id'])
    assert 'sha256:' + hashlib.sha256(data).hexdigest() == matches[0]['digest']
    extract_metadata(data, root)


def download_release(tag, names, root):
    release = api('releases/tags/' + tag)
    assets = {x['name']: x for x in release['assets']}
    for name in names:
        url = 'https://github.com/' + REPO + '/releases/download/' + tag + '/' + name
        assert assets[name]['browser_download_url'] == url
        subprocess.run(['curl', '--fail', '--silent', '--show-error', '--location',
                        '--retry', '3', '--max-time', '600', '--output', str(root / name), url],
                       check=True, timeout=650)
