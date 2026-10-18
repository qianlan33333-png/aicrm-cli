"""Build local candidate archives; never tag, upload or change release channels."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile

PLATFORMS = ["darwin/arm64", "darwin/amd64", "linux/arm64", "linux/amd64", "windows/amd64"]
MODULE = "github.com/qianlan33333-png/aicrm-cli"


def sha(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args]).decode().strip()


def semver(value):
    value = value.removeprefix("v")
    number = r"(?:0|[1-9][0-9]*)"
    if not re.fullmatch(number+r"\."+number+r"\."+number+r"(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", value):
        raise argparse.ArgumentTypeError("version must be SemVer")
    pre = value.split("+", 1)[0].partition("-")[2]
    if any(p.isdigit() and len(p) > 1 and p.startswith("0") for p in pre.split(".")):
        raise argparse.ArgumentTypeError("numeric prerelease identifiers cannot have leading zeroes")
    return value


def build(root, version, output, platforms, go):
    if git(root, "status", "--porcelain"):
        raise ValueError("commit the exact candidate before building")
    record = json.loads((root/"docs/crm-source.json").read_text())
    for row in record["files"]:
        p = root / row["destination"]
        if p.is_symlink() or root not in p.resolve().parents or sha(p) != row["sha256"]:
            raise ValueError("shared core differs from committed V4 export: " + row["destination"])
    if output.exists() and any(output.iterdir()):
        raise ValueError("output directory must be empty; retain previous candidate intact")
    output.mkdir(parents=True, exist_ok=True)
    head, tree = git(root, "rev-parse", "HEAD"), git(root, "rev-parse", "HEAD^{tree}")
    docs = [Path(p) for p in git(root, "ls-files", "--", "README.md", "docs").splitlines()]
    for path in docs:
        p = root/path
        if p.is_symlink() or root not in p.resolve().parents or not p.is_file():
            raise ValueError("distribution documentation must be a regular checkout file")
    artifacts = []
    for platform in platforms:
        system, arch = platform.split("/")
        name = f"aicrm-cli_{version}_{system}_{arch}"
        work = output/name; work.mkdir()
        binary = work/("aicrm-cli.exe" if system == "windows" else "aicrm-cli")
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED="0")
        flags = f"-s -w -X {MODULE}/internal/crmcli.Version={version} -X {MODULE}/internal/crmcli.ClientCommit={head} -X {MODULE}/internal/crmcli.CRMSourceCommit={record['source_head']}"
        subprocess.run([go, "build", "-trimpath", "-buildvcs=true", "-ldflags="+flags, "-o", str(binary), "./cmd/aicrm-cli"], cwd=root, env=env, check=True)
        if system == "windows":
            archive_path = output/(name+".zip")
            with zipfile.ZipFile(archive_path, "w", zipfile.ZIP_DEFLATED) as archive:
                for p, dest in [(binary, binary.name)]+[(root/p, str(p)) for p in docs]:
                    entry = zipfile.ZipInfo(dest, (1980,1,1,0,0,0)); entry.compress_type = zipfile.ZIP_DEFLATED
                    entry.external_attr = ((0o100755 if p == binary else 0o100644) << 16)
                    with p.open("rb") as source, archive.open(entry, "w") as target: shutil.copyfileobj(source, target)
        else:
            archive_path = output/(name+".tar.gz")
            def normalized(info):
                info.uid=info.gid=info.mtime=0;info.uname=info.gname="";return info
            with archive_path.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed, tarfile.open(fileobj=compressed, mode="w") as archive:
                archive.add(binary, arcname=binary.name, filter=normalized)
                for p in docs: archive.add(root/p, arcname=str(p), recursive=False, filter=normalized)
        artifacts.append({"platform":platform,"archive":archive_path.name,"archive_sha256":sha(archive_path),"archive_bytes":archive_path.stat().st_size,"binary":str(binary.relative_to(output)),"binary_sha256":sha(binary)})
    checksums=output/"checksums.txt"
    checksums.write_text("".join(row["archive_sha256"]+"  "+row["archive"]+"\n" for row in sorted(artifacts,key=lambda x:x["archive"])))
    receipt={"schema_version":1,"version":version,"repository":"https://github.com/qianlan33333-png/aicrm-cli","client_commit":head,"client_tree":tree,"crm_source_commit":record["source_head"],"crm_source_tree":record["source_tree"],"payload_sha256":record["payload_sha256"],"catalog_file_sha256":sha(root/"internal/clicatalog/port/catalog.json"),"checksums_sha256":sha(checksums),"artifacts":artifacts,"public_release_created":False,"stable_changed":False}
    (output/"candidate-manifest.json").write_text(json.dumps(receipt,indent=2)+"\n")
    return receipt


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version",required=True,type=semver)
    parser.add_argument("--output",type=Path)
    parser.add_argument("--platform",action="append",choices=PLATFORMS)
    parser.add_argument("--go",default=os.environ.get("AICRM_GO_BINARY","go"))
    args=parser.parse_args();root=Path(__file__).resolve().parents[1]
    result=build(root,args.version,(args.output or root/"dist"/args.version).resolve(),list(dict.fromkeys(args.platform or PLATFORMS)),args.go)
    print(json.dumps(result))


if __name__ == "__main__": main()
