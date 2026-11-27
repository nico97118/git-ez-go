#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
"""Exercise release-integrity failures even when outer checksums are recomputed."""
import argparse
import hashlib
import io
import json
import pathlib
import shutil
import subprocess
import tarfile
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('directory', type=pathlib.Path)
args = parser.parse_args()
assets = args.directory.resolve()
verifier = pathlib.Path(__file__).with_name('verify-release.py')
subprocess.run(['python3', str(verifier), str(assets)], check=True)
archive = next(p for p in assets.glob('*.tar.gz') if not p.name.endswith('_source.tar.gz'))
with tarfile.open(archive) as tar:
    members = [(member, tar.extractfile(member).read()) for member in tar]

cases = {
    'source association': ('BUILD.json', 'Corresponding-source mismatch'),
    'source directions': ('SOURCE.txt', 'Incomplete source directions'),
    'license completeness': ('third_party/go/LICENSE', 'Missing or modified distribution license/document'),
    'SBOM association': ('SBOM.cdx.json', 'SBOM association mismatch'),
}
for label, (suffix, diagnostic) in cases.items():
    with tempfile.TemporaryDirectory(prefix='git-ez-integrity-') as temporary:
        root = pathlib.Path(temporary)
        for file in assets.iterdir():
            if file.name != archive.name:
                shutil.copyfile(file, root / file.name)
        with tarfile.open(root / archive.name, 'w:gz') as tar:
            for member, raw in members:
                if member.name.endswith('/' + suffix):
                    if suffix == 'BUILD.json':
                        value = json.loads(raw)
                        value['source_archive_sha256'] = '0' * 64
                        raw = json.dumps(value).encode()
                    elif suffix == 'SOURCE.txt':
                        raw = b'Source unavailable\n'
                    elif suffix == 'third_party/go/LICENSE':
                        continue
                    else:
                        value = json.loads(raw)
                        for prop in value['metadata']['properties']:
                            if prop['name'] == 'binary.sha256':
                                prop['value'] = '0' * 64
                        raw = json.dumps(value).encode()
                member.size = len(raw)
                tar.addfile(member, io.BytesIO(raw))
        checksums = []
        for file in sorted(root.iterdir()):
            if file.name != 'SHA256SUMS':
                checksums.append(hashlib.sha256(file.read_bytes()).hexdigest() + '  ' + file.name + '\n')
        (root / 'SHA256SUMS').write_text(''.join(checksums))
        result = subprocess.run(['python3', str(verifier), str(root)], capture_output=True, text=True)
        assert result.returncode != 0 and diagnostic in result.stderr, (label, result.stdout, result.stderr)
        print('Refused tampered ' + label)
with tempfile.TemporaryDirectory(prefix='git-ez-missing-source-') as temporary:
    root = pathlib.Path(temporary)
    for file in assets.iterdir():
        if not file.name.endswith('_source.tar.gz'):
            shutil.copyfile(file, root / file.name)
    result = subprocess.run(['python3', str(verifier), str(root)], capture_output=True, text=True)
    assert result.returncode != 0, result.stdout
print('Refused missing corresponding source; five integrity regressions passed')
