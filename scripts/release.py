"""Build reproducible download archives without credentials or local configuration."""
import hashlib,os,pathlib,subprocess,tarfile,zipfile
root=pathlib.Path(__file__).resolve().parents[1]
dist=root/'dist';dist.mkdir(exist_ok=True)
for system,arch in [('darwin','arm64'),('darwin','amd64'),('linux','arm64'),('linux','amd64'),('windows','amd64')]:
 name=f'aicrm-cli_0.1.0_{system}_{arch}'
 work=dist/name;work.mkdir(exist_ok=True)
 binary=work/('aicrm-cli.exe' if system=='windows' else 'aicrm-cli')
 env=dict(os.environ,GOOS=system,GOARCH=arch,CGO_ENABLED='0')
 subprocess.run(['go','build','-trimpath','-ldflags=-s -w','-o',str(binary),'./cmd/aicrm-cli'],cwd=root,env=env,check=True)
 if system=='windows':
  with zipfile.ZipFile(dist/(name+'.zip'),'w',zipfile.ZIP_DEFLATED) as archive:archive.write(binary,binary.name)
 else:
  with tarfile.open(dist/(name+'.tar.gz'),'w:gz') as archive:archive.add(binary,arcname=binary.name)
archives=sorted(list(dist.glob('*.tar.gz'))+list(dist.glob('*.zip')))
(dist/'checksums.txt').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in archives))
print('built',len(archives),'archives')
