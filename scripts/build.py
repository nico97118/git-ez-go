#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Build all production packages/CLI without CGO and retain exact linked modules."""
import argparse, hashlib, json, os, pathlib, platform, subprocess
p = argparse.ArgumentParser()
p.add_argument('--go', default='go')
p.add_argument('--output', required=True)
p.add_argument('--evidence', required=True)
p.add_argument('--version', default='0.1.0-dev')
p.add_argument('--target', choices=[f'{system}/{arch}' for system in ('linux', 'freebsd', 'darwin') for arch in ('amd64', 'arm64')],
               help='Build one target; default builds all six')
a = p.parse_args()
out=pathlib.Path(a.output).resolve(); out.mkdir(parents=True,exist_ok=True)
env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='local',GOAMD64='v1',GOARM64='v8.0',GOEXPERIMENT='',GOFLAGS='')
def run(args, **kw): return subprocess.check_output(args,env=env,text=True,**kw).strip()
records=[]
for system in ('linux','freebsd','darwin'):
 for arch in ('amd64','arm64'):
  if a.target and a.target != f'{system}/{arch}':
   continue
  env.update(GOOS=system,GOARCH=arch)
  subprocess.run([a.go,'build','-buildvcs=false','./...'],env=env,check=True)
  binary=out/f'git-ez-{system}-{arch}'
  subprocess.run([a.go,'build','-buildvcs=false','-trimpath','-ldflags','-s -w -buildid= -X main.version='+a.version,'-o',str(binary),'./cmd/git-ez'],env=env,check=True)
  info=run([a.go,'version','-m',str(binary)])
  linked=json.loads(run([a.go,'version','-m','-json',str(binary)]))
  records.append(dict(linked_modules=linked.get('Deps',[]),target=f'{system}/{arch}',cgo_enabled=False,all_packages_built=True,sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),bytes=binary.stat().st_size,build_info='git-ez: '+info.split(': ',1)[-1]))
for key in ('GOOS','GOARCH'): env.pop(key,None)
modules=run([a.go,'list','-mod=mod','-m','all']).splitlines()
packages=run([a.go,'list','-deps','-f','{{if not .Standard}}{{.ImportPath}}{{end}}','./...']).splitlines()
result=dict(toolchain=run([a.go,'version']),host=platform.system().lower()+'/'+platform.machine(),modules=modules,nonstandard_packages=[x for x in packages if x],builds=records,native_execution_targets=[])
pathlib.Path(a.evidence).write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'targets':len(records),'graph_external_modules':len(modules)-1,'linked_external_modules':{r['target']:len(r['linked_modules']) for r in records},'all_packages_built':True}))
