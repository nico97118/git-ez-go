#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 git-ez contributors
# SPDX-License-Identifier: GPL-3.0-only
"""Inventory actual CGO-disabled binaries; graph modules are not linked modules."""
import argparse,csv,hashlib,json,pathlib,struct,subprocess
p=argparse.ArgumentParser();p.add_argument('--go',default='go');p.add_argument('--builds',type=pathlib.Path,required=True);p.add_argument('--evidence',type=pathlib.Path,required=True);p.add_argument('--version',default='0.1.0-dev');a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[1];a.evidence.mkdir(parents=True,exist_ok=True);licenses={}
for row in csv.reader((root/'third_party/licenses.csv').open()):
 if len(row)==3:licenses.setdefault(row[0],set()).add(row[2])
notices=[]
for directory in ('third_party/licenses','third_party/go'):
 for f in sorted((root/directory).rglob('*')):
  if f.is_file():notices.append({'path':str(f.relative_to(root)),'sha256':hashlib.sha256(f.read_bytes()).hexdigest()})
(a.evidence/'license-texts.json').write_text(json.dumps(notices,indent=2)+'\n')
records=[]
for system in ('linux','freebsd','darwin'):
 for arch in ('amd64','arm64'):
  binary=a.builds/f'git-ez-{system}-{arch}';info=json.loads(subprocess.check_output([a.go,'version','-m','-json',str(binary)],text=True));data=binary.read_bytes();settings={x['Key']:x['Value'] for x in info.get('Settings',[])};assert settings['CGO_ENABLED']=='0'
  record={'target':f'{system}/{arch}','sha256':hashlib.sha256(data).hexdigest(),'linked_external_modules':len(info['Deps'])};components=[]
  for module in info['Deps']:
   types=licenses.get(module['Path'],set())
   # go-licenses may report a subpackage rather than the module root.
   if not types:
    for name,values in licenses.items():
     if name.startswith(module['Path']+'/'):types.update(values)
   assert types,(module['Path'],'unreviewed license classification')
   components.append({'type':'library','name':module['Path'],'version':module['Version'],'bom-ref':module['Path']+'@'+module['Version'],'purl':'pkg:golang/'+module['Path']+'@'+module['Version'],'licenses':[{'license':{'id':license_id}} for license_id in sorted(types)],'properties':[{'name':'go.module.sum','value':module.get('Sum','')}]})
  bom={'bomFormat':'CycloneDX','specVersion':'1.5','version':1,'metadata':{'component':{'type':'application','name':'git-ez','version':a.version,'licenses':[{'license':{'id':'GPL-3.0-only'}}]},'properties':[{'name':'target','value':record['target']},{'name':'binary.sha256','value':record['sha256']},{'name':'go.version','value':info['GoVersion']},{'name':'cgo.enabled','value':'0'}]},'components':components,'dependencies':[{'ref':'git-ez-go','dependsOn':[x['bom-ref'] for x in components]}]}
  bom['metadata']['component']['bom-ref']='git-ez-go'
  (a.evidence/f'{system}-{arch}.cdx.json').write_text(json.dumps(bom,indent=2)+'\n');(a.evidence/f'{system}-{arch}.buildinfo.json').write_text(json.dumps(info,indent=2)+'\n')
  if data[:4]==b'\x7fELF':
   off=struct.unpack_from('<Q',data,32)[0];size,count=struct.unpack_from('<HH',data,54);types=[struct.unpack_from('<I',data,off+i*size)[0] for i in range(count)];assert 2 not in types and 3 not in types;record['elf_interpreter']=False;record['elf_dynamic_segment']=False
  elif system=='darwin':
   assert struct.unpack_from('<I',data)[0]==0xfeedfacf
   count=struct.unpack_from('<I',data,16)[0];offset=32;libs=[]
   for _ in range(count):
    cmd,size=struct.unpack_from('<II',data,offset);assert size>=8 and offset+size<=len(data)
    if cmd in (0xc,0x80000018,0x8000001f,0x80000023):
     name=struct.unpack_from('<I',data,offset+8)[0];assert 24<=name<size
     libs.append(data[offset+name:offset+size].split(b'\0',1)[0].decode())
    offset+=size
   assert all(x.startswith(('/usr/lib/','/System/Library/')) for x in libs);record['shared_libraries']=libs
  records.append(record)
(a.evidence/'distribution-inventory.json').write_text(json.dumps({'builds':records,'license_texts':len(notices),'runtime_prerequisites':['native Git','host OS'],'not_bundled':['Python reference SDKs','ssh-keygen (signature tests only)'],'release_approval':False},indent=2)+'\n')
print(json.dumps({'targets':len(records),'linked_modules':sorted({x['linked_external_modules'] for x in records}),'license_texts':len(notices)}))
