#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Exercise offline schema-4 consultation and native pager conventions via a PTY."""
import argparse, hashlib, json, os, pathlib, pty, select, shutil, subprocess, sys, tempfile, time
p=argparse.ArgumentParser();p.add_argument('--binary',type=pathlib.Path,required=True);p.add_argument('--evidence',type=pathlib.Path,required=True);a=p.parse_args();binary=a.binary.resolve();cases=[]
fixture=pathlib.Path(__file__).resolve().parents[1]/'internal/audit/testdata/python-v4'
with tempfile.TemporaryDirectory(prefix='git-ez-audit-smoke-') as temporary:
 root=pathlib.Path(temporary);repo=root/'repo';repo.mkdir();bin_dir=root/"bin space's";bin_dir.mkdir();shutil.copy2(binary,bin_dir/'git-ez')
 env={k:v for k,v in os.environ.items() if not k.startswith(('GIT_','GENKIT_','OTEL_')) and k not in ('OPENAI_API_KEY','OLLAMA_API_KEY','PAGER','LESS')};env.update(HOME=str(root),PATH=str(bin_dir)+':'+env['PATH'],GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',GENKIT_ENV='dev',GENKIT_REFLECTION_SERVER='true')
 def git(*argv):return subprocess.run(['git',*argv],cwd=repo,env=env,capture_output=True,text=True,check=True)
 git('init','-b','main');git('config','ez.provider','invalid-provider');git('config','ez.endpoint','not-an-endpoint')
 destination=repo/'.git/ez/runs/python-v4';destination.parent.mkdir(parents=True);shutil.copytree(fixture,destination)
 before={str(p.relative_to(destination)):hashlib.sha256(p.read_bytes()).hexdigest() for p in destination.rglob('*') if p.is_file()}
 for argv in [['audit','list'],['audit','show'],['audit','show','python'],['audit','show','--verbose'],['audit','show','--path',str(destination),'--verbose']]:
  result=subprocess.run([str(binary),*argv],cwd=repo,env=env,capture_output=True,text=True,timeout=15);assert result.returncode==0,(argv,result.stderr)
  assert ('python-v4' in result.stdout) and ('synthetic-reference-secret' not in result.stdout)
  if '--verbose' in argv:assert 'git diff' in result.stdout and 'langchain' in result.stdout
  cases.append(dict(case='redirected '+' '.join(argv[:3]),exit_code=0,pager_invocations=0))
 outside=root/'outside';outside.mkdir();result=subprocess.run([str(binary),'audit','show','--path',str(destination)],cwd=outside,env=env,capture_output=True,text=True,timeout=15);assert result.returncode==0 and '0.95' in result.stdout
 cases.append(dict(case='copied path outside repository without provider configuration',exit_code=0))
 no_git=dict(env,PATH='/nonexistent');result=subprocess.run([str(binary),'audit','show','--path',str(destination)],cwd=outside,env=no_git,capture_output=True,text=True,timeout=15);assert result.returncode==0 and '0.95' in result.stdout;cases.append(dict(case='explicit path without Git executable or provider configuration',exit_code=0))
 marker=root/'pager-count';pager=root/'pager.py';pager.write_text('import pathlib,sys,os\np=pathlib.Path(sys.argv[1])\np.write_text(p.read_text()+"x" if p.exists() else "x")\nsys.stdout.write(sys.stdin.read())\n')
 import shlex
 command=shlex.join([sys.executable,str(pager),str(marker)]);git('config','core.pager',command)
 def terminal(argv,expected,pager_count,environment=None):
  marker.unlink(missing_ok=True);master,slave=pty.openpty();process=subprocess.Popen(argv,cwd=repo,env=environment or env,stdin=subprocess.DEVNULL,stdout=slave,stderr=slave);os.close(slave);chunks=[];deadline=time.monotonic()+20
  try:
   while True:
    if time.monotonic()>deadline:process.kill();raise AssertionError('pager timed out')
    if select.select([master],[],[],.1)[0]:
     try:data=os.read(master,65536)
     except OSError:break
     if not data:break
     chunks.append(data)
    elif process.poll() is not None:break
   code=process.wait(timeout=5)
  finally:os.close(master)
  output=b''.join(chunks).decode(errors='replace');assert code==0,(argv,code,output)
  if expected:assert expected in output,(argv,output)
  else:assert '# git-ez' not in output,'quitting pager replayed report'
  count=len(marker.read_text()) if marker.exists() else 0;assert count==pager_count,(argv,count,output)
  cases.append(dict(case='terminal '+' '.join(argv[1:4]),exit_code=code,pager_invocations=count))
 for options,count in [([],1),(['--no-pager'],0),(['--paginate'],1)]:terminal(['git',*options,'ez','audit','show'],'# git-ez',count)
 git('config','pager.ez','false');terminal([str(binary),'audit','show'],'# git-ez',0)
 override=dict(env,GIT_PAGER=command);terminal([str(binary),'audit','show'],'# git-ez',1,override)
 missing=dict(env,GIT_PAGER='git-ez-no-such-pager --options');terminal([str(binary),'audit','show'],'# git-ez',0,missing)
 # Force stdin pipe pressure so an early pager exit cannot silently replay the report.
 data=json.loads((destination/'run.json').read_text());data['reviews'][0]['resolution']['rationale']='x'*200000;(destination/'run.json').write_text(json.dumps(data))
 quitting=dict(env,GIT_PAGER='exit 0');terminal([str(binary),'audit','show'],None,0,quitting)
 # Consultation itself never rewrote evidence; only this explicit fixture edit differs.
 after={str(p.relative_to(destination)):hashlib.sha256(p.read_bytes()).hexdigest() for p in destination.rglob('*') if p.is_file()};assert all(after[k]==v for k,v in before.items() if k!='run.json')
a.evidence.parent.mkdir(parents=True,exist_ok=True);a.evidence.write_text(json.dumps(dict(binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),real_model_requests=0,cases=cases),indent=2)+'\n');print(json.dumps(dict(audit_cases=len(cases),failures=0)))
