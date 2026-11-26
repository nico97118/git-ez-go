#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Smoke-test the installed interactive CLI with local scripted native protocols."""
import argparse, hashlib, http.server, json, os, pathlib, select, signal, subprocess, tempfile, threading, time, types
p=argparse.ArgumentParser();p.add_argument('--binary',type=pathlib.Path,required=True);p.add_argument('--evidence',type=pathlib.Path,required=True);a=p.parse_args()
binary=a.binary.resolve();results=[]
with tempfile.TemporaryDirectory(prefix='git-ez-interactive-smoke-') as temporary:
 root=pathlib.Path(temporary)
 bin_dir=root/'bin';bin_dir.mkdir();(bin_dir/'git-ez').symlink_to(binary)
 env={k:v for k,v in os.environ.items() if not k.startswith(('GIT_','GENKIT_','OTEL_')) and k not in ('OPENAI_API_KEY','OLLAMA_API_KEY')}
 env.update(HOME=str(root),GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_NOSYSTEM='1',GIT_EDITOR='true',GENKIT_ENV='prod',PATH=str(bin_dir)+':'+env['PATH'])
 for provider in ('openai','ollama'):
  for action in ('accept','quit','revise','eof','validation','interrupt'):
   directory=root/(provider+'-'+action);directory.mkdir();requests=[]
   class Model(http.server.BaseHTTPRequestHandler):
    def log_message(self,*args):pass
    def do_POST(self):
     request=json.loads(self.rfile.read(int(self.headers['Content-Length'])));requests.append(request);number=len(requests);phase=(number-1)%3
     name=('read_file','write_file','finish')[phase];arguments={'filePath':'file.txt'}
     if phase==1:arguments['content']='bad\n' if action=='validation' and number<4 else 'combined\n'
     if phase==2:arguments={'rationale':'Preserve both branch intents; independent checks requested where configured.','confidence':.95}
     if provider=='ollama':response={'model':'fixture','done':True,'done_reason':'stop','message':{'role':'assistant','content':'','tool_calls':[{'function':{'name':name,'arguments':arguments}}]},'prompt_eval_count':10,'eval_count':1}
     else:response={'id':'fixture','object':'chat.completion','model':'fixture','choices':[{'index':0,'finish_reason':'tool_calls','message':{'role':'assistant','content':'','tool_calls':[{'id':'call-'+str(number),'type':'function','function':{'name':name,'arguments':json.dumps(arguments)}}]}}],'usage':{'prompt_tokens':10,'completion_tokens':1}}
     payload=json.dumps(response).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(payload)));self.end_headers();self.wfile.write(payload)
   server=http.server.HTTPServer(('127.0.0.1',0),Model);thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
   def git(*argv,check=True):return subprocess.run(['git',*argv],cwd=directory,env=env,capture_output=True,text=True,check=check).stdout.strip()
   try:
    git('init','-b','main');git('config','user.name','Test');git('config','user.email','test@example.invalid');git('config','commit.gpgSign','false')
    file=directory/'file.txt';file.write_text('base\n');git('add','file.txt');git('commit','-m','base');git('checkout','-b','feature');file.write_text('feature\n');git('commit','-am','feature');git('checkout','main');file.write_text('main\n');git('commit','-am','main')
    git('-c','merge.conflictStyle=diff3','merge','--no-autostash','--no-edit',git('rev-parse','feature'),check=False);marked=file.read_bytes();index=git('ls-files','--stage','-z');git('merge','--abort')
    git('config','ez.model','fixture');git('config','ez.provider',provider);suffix='/v1/chat/completions' if provider=='openai' else '/api/chat';git('config','ez.endpoint',f'http://127.0.0.1:{server.server_port}'+suffix)
    answers={'accept':'a\n','quit':'q\n','revise':'r\nkeep both intents\na\n','eof':'','validation':'a\n','interrupt':''}[action]
    argv=[str(binary),'merge','feature','--interactive']
    if action=='validation':argv+=['--check','/bin/sh','-c','printf check-feedback; grep -qx combined file.txt']
    if action=='interrupt':
     child=subprocess.Popen(['git','ez',*argv[1:]],cwd=directory,env=env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,start_new_session=True)
     output=bytearray();deadline=time.monotonic()+60
     while b'[q] Quit:' not in output and time.monotonic()<deadline:
      readable,_,_=select.select([child.stdout],[],[],.1)
      if readable:
       data=os.read(child.stdout.fileno(),65536)
       if not data:break
       output.extend(data)
     assert b'[q] Quit:' in output,(provider,output)
     child.send_signal(signal.SIGINT);child.wait(timeout=15);tail,_=child.communicate();output.extend(tail)
     result=types.SimpleNamespace(returncode=child.returncode,stdout=output.decode(errors='replace'),stderr='')
    else:result=subprocess.run(argv,cwd=directory,env=env,input=answers,text=True,capture_output=True,timeout=60)
    success=action in ('accept','revise','validation');assert result.returncode==(-signal.SIGINT if action=='interrupt' else 0 if success else 1),(provider,action,result.stdout,result.stderr)
    assert len(requests)==(6 if action in ('revise','validation') else 3),(provider,action,len(requests))
    if success:assert git('status','--porcelain')=='' and file.read_text()=='combined\n'
    else:assert file.read_bytes()==marked and git('ls-files','--stage','-z')==index,(provider,action,marked,file.read_bytes(),index,git('ls-files','--stage','-z'),result.stdout,result.stderr)
    records=list((directory/'.git/ez/runs').glob('*/run.json'));assert len(records)==1;run=json.loads(records[0].read_text());reviews=run['reviews'];assert len(reviews)==(2 if action in ('revise','validation') else 1)
    results.append({'provider':provider,'action':action,'exit_code':result.returncode,'actual_scripted_model_requests':len(requests),'proposals':len(reviews),'result':'passed','native_conflict_restored':not success})
   finally:server.shutdown();server.server_close();thread.join()
a.evidence.parent.mkdir(parents=True,exist_ok=True);a.evidence.write_text(json.dumps({'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'real_model_requests':0,'cases':results},indent=2)+'\n');print(json.dumps({'installed_interactive_cases':len(results),'failures':0}))
