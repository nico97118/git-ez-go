#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Verify flat release assets without extracting or executing payloads."""
import argparse
import hashlib
import json
import pathlib
import re
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument('directory', type=pathlib.Path)
parser.add_argument('--publication', action='store_true')
args = parser.parse_args()
root = args.directory


def digest(stream):
    result = hashlib.sha256()
    for chunk in iter(lambda: stream.read(1024 * 1024), b''):
        result.update(chunk)
    return result.hexdigest()


def require(condition, message):
    if not condition:
        raise SystemExit(message)


listed = {}
for line in (root / 'SHA256SUMS').read_text().splitlines():
    match = re.fullmatch(r'([0-9a-f]{64})  ([A-Za-z0-9_.+-]+)', line)
    require(match is not None, 'Invalid checksum line')
    expected, name = match.groups()
    require(name not in listed, 'Duplicate checksum entry')
    listed[name] = expected
require(set(listed) == {f.name for f in root.iterdir() if f.name != 'SHA256SUMS'},
        'Checksum inventory does not match assets')
for name, expected in listed.items():
    file = root / name
    require(file.is_file() and not file.is_symlink(), 'Invalid asset: ' + name)
    with file.open('rb') as stream:
        require(digest(stream) == expected, 'Checksum mismatch: ' + name)

provenance = json.loads((root / 'PROVENANCE.json').read_text())
require(provenance['product'] == 'git-ez', 'Unexpected product')
if args.publication:
    require(provenance['publishable'] and provenance['source_kind'] == 'commit'
            and re.fullmatch(r'v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', provenance['version']),
            'Assets are not eligible for publication')
archives = list(root.glob('*.tar.gz'))
require(len(archives) == 7, 'Expected seven archives')
targets = set()
source_name = 'git-ez_' + provenance['version'] + '_source.tar.gz'
require(source_name in listed, 'Missing source bundle')
for file in archives:
    with tarfile.open(file) as tar:
        names = tar.getnames()
        require(len(names) == len(set(names)), 'Duplicate tar member')
        prefix = file.name[:-7] + '/'
        if file.name == source_name:
            prefix = 'git-ez-source/'
        for member in tar:
            path = pathlib.PurePosixPath(member.name)
            require(not path.is_absolute() and '..' not in path.parts
                    and member.isfile() and member.name.startswith(prefix),
                    'Unsafe archive member: ' + member.name)
        if file.name == source_name:
            manifest = json.load(tar.extractfile(prefix + 'SOURCE-MANIFEST.json'))
            require(manifest['source_tree'] == provenance['source_tree'], 'Wrong source tree')
            require(set(names) == {prefix + name for name in manifest['files']}
                    | {prefix + 'SOURCE-MANIFEST.json'}, 'Source inventory mismatch')
            for name, expected in manifest['files'].items():
                require(digest(tar.extractfile(prefix + name)) == expected,
                        'Source hash mismatch: ' + name)
            continue
        required = ['git-ez', 'LICENSE', 'NOTICE', 'SOURCE.tar.gz', 'BUILD.json',
                    'SBOM.cdx.json', 'DEPENDENCIES.json']
        require(all(prefix + name in names for name in required), 'Incomplete archive')
        metadata = json.load(tar.extractfile(prefix + 'BUILD.json'))
        require(all(metadata[key] == value for key, value in provenance.items()),
                'Inconsistent provenance')
        target = metadata['target']
        require(target not in targets, 'Duplicate target')
        targets.add(target)
        require(file.name == 'git-ez_' + provenance['version'] + '_'
                + target.replace('/', '_') + '.tar.gz', 'Wrong target filename')
        require(tar.getmember(prefix + 'git-ez').mode == 0o755, 'Binary is not executable')
        require(digest(tar.extractfile(prefix + 'git-ez')) == metadata['binary_sha256'],
                'Binary hash mismatch')
        require(digest(tar.extractfile(prefix + 'SOURCE.tar.gz'))
                == metadata['source_archive_sha256'] == listed[source_name],
                'Corresponding-source mismatch')
require(targets == {system + '/' + arch for system in ['linux', 'freebsd', 'darwin']
                    for arch in ['amd64', 'arm64']}, 'Incomplete target matrix')
print(json.dumps({'checksums_verified': len(listed), 'archives_verified': len(archives),
                  'publishable': provenance['publishable']}))
