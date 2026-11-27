#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Explicit installed-CLI path qualification; no provider or model access."""
import argparse
import json
import os
import pathlib
import subprocess
import tempfile

p = argparse.ArgumentParser()
p.add_argument('--binary', type=pathlib.Path, required=True)
p.add_argument('--reference-binary', type=pathlib.Path)
p.add_argument('--evidence', type=pathlib.Path, required=True)
a = p.parse_args()
env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
env.update(GIT_CONFIG_GLOBAL='/dev/null', GIT_CONFIG_NOSYSTEM='1', GIT_EDITOR='true',
           GIT_PAGER='cat', GIT_AUTHOR_DATE='2000-01-01T00:00:00Z',
           GIT_COMMITTER_DATE='2000-01-01T00:00:00Z')
results = []
implementations = [('go', a.binary.resolve())]
if a.reference_binary:
    implementations.append(('python', a.reference_binary.resolve()))
with tempfile.TemporaryDirectory(prefix='git-ez-path-check-') as temporary:
    for label, binary in implementations:
        for absolute in (False, True):
            base = pathlib.Path(temporary) / (label + str(absolute))
            base.mkdir()
            lexical, physical = base / 'lexical', base / 'physical'
            for repo in (lexical, physical):
                repo.mkdir()
            (physical / 'child').mkdir()

            def git(repo, *args):
                return subprocess.check_output(['git', *args], cwd=repo, env=env,
                                               text=True, stderr=subprocess.DEVNULL).strip()

            for repo in (lexical, physical):
                git(repo, 'init', '-b', 'main')
                for key, value in [('user.name', 'Test'), ('user.email', 'test@example.invalid'),
                                   ('commit.gpgSign', 'false'), ('ez.model', 'unused')]:
                    git(repo, 'config', key, value)
                (repo / 'base').write_text('base\n')
                git(repo, 'add', '.')
                git(repo, 'commit', '-m', 'base')
                git(repo, 'branch', 'feature')
                (repo / 'added').write_text('new\n')
                git(repo, 'add', '.')
                git(repo, 'commit', '-m', 'target')
                git(repo, 'checkout', 'feature')
            # Keep both checkouts clean: track the symlink on each lexical branch.
            (lexical / 'link').symlink_to(physical / 'child')
            git(lexical, 'add', 'link')
            git(lexical, 'commit', '-m', 'link')
            before = [git(repo, 'rev-parse', 'HEAD') for repo in (lexical, physical)]
            argument = str(lexical) + '/link/..' if absolute else 'lexical/link/..'
            native = git(base, '-C', argument, 'rev-parse', '--show-toplevel')
            assert pathlib.Path(native).resolve() == physical.resolve()
            call = subprocess.run([str(binary), 'merge', 'main', '--repo', argument],
                                  cwd=base, env=env, capture_output=True, text=True)
            after = [git(repo, 'rev-parse', 'HEAD') for repo in (lexical, physical)]
            assert call.returncode == 0, (label, call.stdout, call.stderr)
            assert before[0] == after[0], 'Unintended repository changed'
            assert before[1] != after[1], 'Intended repository did not change'
            assert after[1] == git(physical, 'rev-parse', 'main')
            assert git(lexical, 'status', '--porcelain') == ''
            assert git(physical, 'status', '--porcelain') == ''
            results.append(dict(implementation=label, absolute=absolute, exit=call.returncode,
                                intended_changed=True, unintended_unchanged=True,
                                resulting_head=after[1]))
if a.reference_binary:
    assert [r['resulting_head'] for r in results[:2]] == [r['resulting_head'] for r in results[2:]]
a.evidence.parent.mkdir(parents=True, exist_ok=True)
a.evidence.write_text(json.dumps(dict(cases=results, real_model_requests=0), indent=2) + '\n')
print(json.dumps(dict(cases=len(results), passed=True)))
