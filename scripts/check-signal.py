#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Qualify direct and actual Git-extension cancellation using synthetic validation."""
import argparse,fcntl,hashlib,json,os,pathlib,pty,select,signal,subprocess,sys,tempfile,termios,time
p=argparse.ArgumentParser();p.add_argument('--binary',type=pathlib.Path,required=True);p.add_argument('--evidence',type=pathlib.Path,required=True);a=p.parse_args();binary=a.binary.resolve();cases=[]
with tempfile.TemporaryDirectory(prefix='git-ez-signal-')as temporary:
 root=pathlib.Path(temporary);bindir=root/'bin';bindir.mkdir();(bindir/'git-ez').symlink_to(binary)
 env={k:v for k,v in os.environ.items()if not k.startswith(('GIT_','GENKIT_','OTEL_'))and not k.endswith(('API_KEY','API_TOKEN'))};env.update(HOME=str(root),PATH=str(bindir)+':'+env['PATH'],GIT_CONFIG_NOSYSTEM='1',GIT_CONFIG_GLOBAL='/dev/null',GENKIT_ENV='prod')
 for mode in ['direct','git-parent','git-terminal']:
  repo=root/mode;repo.mkdir()
  def git(*args):return subprocess.run(['git',*args],cwd=repo,env=env,capture_output=True,text=True,check=True)
  git('init','-b','main');git('config','user.name','Qualification');git('config','user.email','qualification@example.invalid');git('config','ez.provider','ollama');git('config','ez.model','unused');(repo/'file').write_text('base\n');git('add','.');git('commit','-m','base')
  ready=root/(mode+'-ready');late=root/(mode+'-late');helper='import pathlib,time;pathlib.Path('+repr(str(ready))+').write_text("ready");time.sleep(1.5);pathlib.Path('+repr(str(late))+').write_text("late")'
  args=([str(binary)]if mode=='direct'else['git','ez'])+['merge','HEAD','--check',sys.executable,'-B','-c',helper]
  master=slave=None
  if mode=='git-terminal':
   master,slave=pty.openpty()
   def control_terminal():os.setsid();fcntl.ioctl(0,termios.TIOCSCTTY,0)
   child=subprocess.Popen(args,cwd=repo,env=env,stdin=slave,stdout=slave,stderr=slave,preexec_fn=control_terminal);os.close(slave)
  else:child=subprocess.Popen(args,cwd=repo,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
  for _ in range(400):
   if ready.exists():break
   if child.poll()is not None:raise RuntimeError((mode,'early exit'))
   time.sleep(.02)
  assert ready.exists(),mode
  if master is None:child.send_signal(signal.SIGINT);stdout,stderr=child.communicate(timeout=15)
  else:
   os.write(master,b'\x03');output=bytearray();deadline=time.monotonic()+15
   while time.monotonic()<deadline:
    available,_,_=select.select([master],[],[],.1)
    if available:
     try:data=os.read(master,65536)
     except OSError:break
     if not data:break
     output.extend(data)
    elif child.poll()is not None:break
   child.wait(timeout=2);os.close(master);stdout=output.decode(errors='replace');stderr=''
  time.sleep(1.7);clean=not git('status','--porcelain','--untracked-files=no').stdout;stopped=not late.exists();audit=subprocess.run([str(binary),'audit','list'],cwd=repo,env=env,capture_output=True,text=True)
  assert stopped and clean,(mode,stdout,stderr);assert child.returncode!=0,(mode,stdout,stderr)
  if mode=='direct':assert child.returncode==1,(stdout,stderr)
  else:assert child.returncode==-signal.SIGINT,(mode,child.returncode,stdout,stderr)
  assert 'waitpid' not in stdout+stderr,(mode,stdout,stderr)
  assert audit.returncode==0 and not(repo/'.git/ez/runs').exists(),audit
  cases.append(dict(mode=mode,exit_code=child.returncode,stdout=stdout,stderr=stderr,managed_child_stopped=stopped,tracked_clean=clean,persistent_audit=False))
a.evidence.parent.mkdir(parents=True,exist_ok=True);a.evidence.write_text(json.dumps(dict(binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),git=subprocess.check_output(['git','--version'],text=True).strip(),cases=cases,real_model_requests=0),indent=2)+'\n');print(json.dumps(cases))
