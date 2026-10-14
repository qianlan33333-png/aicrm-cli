"""Build download archives without credentials or local configuration."""
import argparse,hashlib,os,pathlib,subprocess,tarfile,zipfile
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--version',default='0.1.3')
version=parser.parse_args().version.removeprefix('v')
root=pathlib.Path(__file__).resolve().parents[1]
dist=root/'dist';dist.mkdir(exist_ok=True)
archives=[]
for system,arch in [('darwin','arm64'),('darwin','amd64'),('linux','arm64'),('linux','amd64'),('windows','amd64')]:
 name=f'aicrm-cli_{version}_{system}_{arch}'
 work=dist/name;work.mkdir(exist_ok=True)
 binary=work/('aicrm-cli.exe' if system=='windows' else 'aicrm-cli')
 env=dict(os.environ,GOOS=system,GOARCH=arch,CGO_ENABLED='0')
 flags=f'-s -w -X github.com/qianlan33333-png/aicrm-cli/internal/crmcli.Version={version}'
 subprocess.run(['go','build','-trimpath','-ldflags='+flags,'-o',str(binary),'./cmd/aicrm-cli'],cwd=root,env=env,check=True)
 if system=='windows':
  archive_path=dist/(name+'.zip')
  with zipfile.ZipFile(archive_path,'w',zipfile.ZIP_DEFLATED) as archive:archive.write(binary,binary.name)
 else:
  archive_path=dist/(name+'.tar.gz')
  with tarfile.open(archive_path,'w:gz') as archive:archive.add(binary,arcname=binary.name)
 archives.append(archive_path)
archives.sort()
(dist/'checksums.txt').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in archives))
print('built',len(archives),'archives')
