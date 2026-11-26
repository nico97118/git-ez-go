#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Build verifiable, self-contained distribution archives. Never publishes."""
import argparse
import gzip
import hashlib
import io
import json
import os
import pathlib
import re
import shutil
import subprocess
import tarfile
import tempfile
ROOT = pathlib.Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--go', default='go')
p.add_argument('--revision', default='HEAD')
p.add_argument('--snapshot', action='store_true', help='Dry-run the explicitly staged tree; never publishable')
p.add_argument('--output', type=pathlib.Path, required=True)
a = p.parse_args()
output = a.output.resolve()
output.mkdir(parents=True, exist_ok=True)
if any(output.iterdir()):
    raise SystemExit('Output directory must be empty')

def command(args, cwd=ROOT, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True).strip()

def git(*args):
    return command(['git', *args])
if a.snapshot:
    tree = git('write-tree')
    revision = None
    epoch = 0
    version = '0.0.0-snapshot.' + tree[:12]
    tag = None
else:
    revision = git('rev-parse', '--verify', a.revision + '^{commit}')
    tree = git('rev-parse', revision + '^{tree}')
    epoch = int(git('show', '-s', '--format=%ct', revision))
    tags = git('tag', '--points-at', revision).splitlines()
    tags = [t for t in tags if re.fullmatch('v\\d+\\.\\d+\\.\\d+(?:-[0-9A-Za-z.-]+)?', t)]
    tag = tags[0] if len(tags) == 1 else None
    version = tag or '0.0.0-dev.' + revision[:12]
assert re.fullmatch('[0-9a-f]{40,64}', tree)
toolchain = command([a.go, 'env', 'GOVERSION'])
provenance = {
    'product': 'git-ez',
    'version': version,
    'source_commit': revision,
    'source_tree': tree,
    'source_kind': 'staged-tree' if a.snapshot else 'commit',
    'tag': tag,
    'publishable': bool(tag and revision),
    'source_date_epoch': epoch,
    'go_version': toolchain,
    'cgo_enabled': False,
    'flags': ['-trimpath', '-buildvcs=false', '-ldflags=-buildid= -X main.version=' + version],
    'repository': os.environ.get('GITHUB_REPOSITORY'),
    'workflow_run': os.environ.get('GITHUB_RUN_ID'),
    'provenance_signed': False,
}

def dump(path, value):
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')

def sha(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def archive(path, entries):
    with path.open('wb') as raw, gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=epoch) as compressed, tarfile.open(fileobj=compressed, mode='w') as tar:
        for name, file in sorted(entries):
            info = tarfile.TarInfo(name)
            info.uid = info.gid = 0
            info.uname = info.gname = ''
            info.mtime = epoch
            info.mode = 0o755 if file.name == 'git-ez' else 0o644
            info.size = file.stat().st_size
            with file.open('rb') as data:
                tar.addfile(info, data)
with tempfile.TemporaryDirectory(prefix='git-ez-release-') as temporary:
    temporary = pathlib.Path(temporary)
    source = temporary / 'source'
    source.mkdir()
    raw = subprocess.check_output(['git', 'archive', tree], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(raw)) as tar:
        tar.extractall(source, filter='data')
    required = (source / '.go-version').read_text().strip()
    if toolchain != 'go' + required:
        raise SystemExit('Release builds require exact Go ' + required)
    env = dict(os.environ, GOTOOLCHAIN='local', GOFLAGS='', GOTELEMETRY='off')
    subprocess.run([a.go, 'mod', 'download'], cwd=source, env=env, check=True)
    subprocess.run([a.go, 'mod', 'verify'], cwd=source, env=env, check=True)
    graph = []
    decoder = json.JSONDecoder()
    text = command([a.go, 'list', '-m', '-json', 'all'], source, env)
    while text.strip():
        module, end = decoder.raw_decode(text.lstrip())
        graph.append({k: module[k] for k in ['Path', 'Version', 'Sum', 'GoModSum', 'GoVersion', 'Indirect'] if k in module})
        text = text.lstrip()[end:]
    subprocess.run([a.go, 'mod', 'vendor'], cwd=source, env=env, check=True)
    builds = temporary / 'builds'
    evidence = temporary / 'metadata'
    evidence.mkdir()
    # Build the selected tree, independently of unrelated working-tree changes.
    subprocess.run(['python3', str(source / 'scripts/build.py'), '--go', a.go, '--output', str(builds), '--evidence', str(evidence / 'builds.json'), '--version', version], cwd=source, env=env, check=True)
    subprocess.run(['python3', str(source / 'scripts/inventory.py'), '--go', a.go, '--builds', str(builds), '--evidence', str(evidence), '--version', version], cwd=source, env=env, check=True)
    dump(evidence / 'DEPENDENCIES.json', graph)
    dump(evidence / 'PROVENANCE.json', provenance)
    # Deliver exact Go runtime/stdlib source alongside vendored module sources.
    goroot = pathlib.Path(command([a.go, 'env', 'GOROOT']))
    stdlib = source / 'toolchain-source'
    stdlib.mkdir()
    shutil.copytree(goroot / 'src', stdlib / 'src')
    for name in ['LICENSE', 'PATENTS', 'VERSION', 'go.env']:
        if (goroot / name).is_file():
            shutil.copy2(goroot / name, stdlib / name)
    (stdlib / 'README.txt').write_text('Exact Go standard-library/runtime sources for ' + toolchain + '. Use the matching Go SDK to rebuild. The Go compiler executable is not bundled.\n')
    shutil.copy2(evidence / 'DEPENDENCIES.json', source / 'DEPENDENCIES.json')
    shutil.copy2(evidence / 'PROVENANCE.json', source / 'PROVENANCE.json')
    files = [f for f in source.rglob('*') if f.is_file()]
    dump(source / 'SOURCE-MANIFEST.json', {'source_tree': tree, 'files': {str(f.relative_to(source)): sha(f) for f in sorted(files)}})
    source_name = 'git-ez_' + version + '_source.tar.gz'
    source_archive = output / source_name
    archive(source_archive, [(str(pathlib.Path('git-ez-source') / f.relative_to(source)), f) for f in source.rglob('*') if f.is_file()])
    for system in ['linux', 'freebsd', 'darwin']:
        for arch in ['amd64', 'arm64']:
            staging = temporary / (system + '-' + arch)
            staging.mkdir()
            shutil.copy2(builds / f'git-ez-{system}-{arch}', staging / 'git-ez')
            for name in ['README.md', 'LICENSE', 'NOTICE', 'SECURITY.md']:
                shutil.copy2(source / name, staging / name)
            shutil.copytree(source / 'docs', staging / 'docs')
            shutil.copytree(source / 'third_party', staging / 'third_party')
            shutil.copy2(source_archive, staging / 'SOURCE.tar.gz')
            shutil.copy2(evidence / f'{system}-{arch}.cdx.json', staging / 'SBOM.cdx.json')
            shutil.copy2(evidence / 'DEPENDENCIES.json', staging / 'DEPENDENCIES.json')
            dump(staging / 'BUILD.json', {**provenance, 'target': system + '/' + arch, 'binary_sha256': sha(staging / 'git-ez'), 'source_archive': source_name, 'source_archive_sha256': sha(source_archive), 'build_info': json.loads((evidence / f'{system}-{arch}.buildinfo.json').read_text())})
            prefix = 'git-ez_' + version + '_' + system + '_' + arch
            archive(output / (prefix + '.tar.gz'), [(str(pathlib.Path(prefix) / f.relative_to(staging)), f) for f in staging.rglob('*') if f.is_file()])
    shutil.copy2(evidence / 'PROVENANCE.json', output / 'PROVENANCE.json')
    shutil.copy2(evidence / 'DEPENDENCIES.json', output / 'DEPENDENCIES.json')
    (output / 'SHA256SUMS').write_text(''.join((sha(f) + '  ' + f.name + '\n' for f in sorted(output.iterdir()) if f.is_file())))
print(json.dumps({'archives': 7, 'version': version, 'source_tree': tree, 'source_commit': revision, 'publishable': provenance['publishable'], 'published': False}))
