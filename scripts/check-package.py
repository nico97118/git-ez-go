#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Qualify native archive discovery and an uncached, offline vendored rebuild."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('directory', type=pathlib.Path)
parser.add_argument('--go', default='go')
parser.add_argument('--evidence', type=pathlib.Path, required=True)
args = parser.parse_args()
assets = args.directory.resolve()
scripts = pathlib.Path(__file__).resolve().parent
subprocess.run(['python3', str(scripts / 'verify-release.py'), str(assets)], check=True)
metadata = json.loads((assets / 'PROVENANCE.json').read_text())
system = {'Darwin': 'darwin', 'Linux': 'linux', 'FreeBSD': 'freebsd'}[platform.system()]
arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'amd64': 'amd64', 'x86_64': 'amd64'}[platform.machine()]
name = 'git-ez_' + metadata['version'] + '_' + system + '_' + arch
with tempfile.TemporaryDirectory(prefix='git-ez-package-check-') as temporary:
    root = pathlib.Path(temporary)
    with tarfile.open(assets / (name + '.tar.gz')) as tar:
        tar.extractall(root, filter='data')
    package = root / name
    binary = package / 'git-ez'
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('GIT_', 'GENKIT_', 'OTEL_'))
           and not key.endswith(('API_KEY', 'API_TOKEN'))}
    home = root / 'home'
    home.mkdir()
    bindir = root / 'bin'
    bindir.mkdir()
    (bindir / 'git-ez').symlink_to(binary)
    env.update(HOME=str(home), PATH=str(bindir) + os.pathsep + env['PATH'],
               GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1',
               GOTOOLCHAIN='local', GOTELEMETRY='off')
    version = subprocess.check_output(['git', 'ez', '--version'], env=env, text=True).strip()
    assert metadata['version'] in version, version
    subprocess.run([str(binary), '--help'], env=env, check=True, stdout=subprocess.DEVNULL)
    # Complete source is already authenticated by the outer and nested hashes.
    with tarfile.open(package / 'SOURCE.tar.gz') as tar:
        tar.extractall(root, filter='data')
    source = root / 'git-ez-source'
    sdk = subprocess.check_output([args.go, 'env', 'GOVERSION'], env=env, text=True).strip()
    assert sdk == metadata['go_version'], 'Use the exact recorded Go SDK'
    env.update(GOPATH=str(root / 'empty-gopath'), GOCACHE=str(root / 'empty-gocache'),
               GOMODCACHE=str(root / 'empty-modcache'), GOPROXY='off', GOSUMDB='off',
               CGO_ENABLED='0', GOFLAGS='', GOEXPERIMENT='', GOAMD64='v1', GOARM64='v8.0')
    rebuilt = root / 'rebuilt'
    subprocess.run([args.go, 'build', '-mod=vendor', '-buildvcs=false', '-trimpath',
                    '-ldflags', '-buildid= -X main.version=' + metadata['version'],
                    '-o', str(rebuilt), './cmd/git-ez'], cwd=source, env=env, check=True)
    sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
    assert sha(rebuilt) == sha(binary), 'Offline rebuild differs from distributed binary'
    result = {'target': system + '/' + arch, 'version': version,
              'native_git_discovery': True, 'offline_empty_cache_rebuild': True,
              'byte_identical_binary': True, 'sha256': sha(binary), 'real_model_requests': 0}
args.evidence.parent.mkdir(parents=True, exist_ok=True)
args.evidence.write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps(result))
