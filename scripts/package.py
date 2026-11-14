#!/usr/bin/env python3
"""Build a source-and-binary archive from an explicit, credential-free file list."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", args.version):
        parser.error("version must be a semantic version")
    name = f"agentgate-{args.version}"
    destination = ROOT / "dist" / name
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True)
    directories = ["cmd", "internal", "docs", "examples", "scripts", ".github",
                   "design-kit/bin", "design-kit/lib", "design-kit/skills", "design-kit/tests"]
    for directory in directories:
        shutil.copytree(ROOT / directory, destination / directory,
                        ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    names = ["README.md", "README.ru.md", "LICENSE", "CHANGELOG.md", "CHANGELOG.ru.md",
             "go.mod", "Makefile", ".gitignore", "SECURITY.md", "RELEASE_NOTES.md",
             "design-kit/embed.go", "design-kit/embed_test.go", "design-kit/README.md",
             "design-kit/package.json", "design-kit/package-lock.json"]
    for filename in names:
        shutil.copy2(ROOT / filename, destination / filename)
    for arch in ["amd64", "arm64"]:
        target = destination / "bin" / f"linux-{arch}" / "agentgate"
        target.parent.mkdir(parents=True)
        shutil.copy2(ROOT / "dist" / f"linux-{arch}" / "agentgate", target)
        target.chmod(0o755)
    commit = "source-archive"
    modified = None
    try:
        commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True, stderr=subprocess.DEVNULL).strip()
        modified = bool(subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=normal"],
                                               cwd=ROOT, text=True, stderr=subprocess.DEVNULL).strip())
    except (OSError, subprocess.CalledProcessError):
        previous = ROOT / "RELEASE.json"
        if previous.exists():
            commit = json.loads(previous.read_text()).get("base_commit", "source-archive")
    metadata = {"version": args.version, "repository": "https://github.com/knaus94/agentgate",
                "base_commit": commit, "modified_from_base": modified,
                "platforms": ["linux/amd64", "linux/arm64"], "source_included": True}
    (destination / "RELEASE.json").write_text(json.dumps(metadata, indent=2) + "\n")
    checksums = []
    for file in sorted(destination.rglob("*")):
        if file.is_file():
            checksums.append(f"{hashlib.sha256(file.read_bytes()).hexdigest()}  {file.relative_to(destination).as_posix()}")
    (destination / "SHA256SUMS").write_text("\n".join(checksums) + "\n")
    archive = ROOT / "dist" / f"{name}.zip"
    # ZIP has a 1980 lower bound. A fixed timestamp makes equivalent builds reproducible.
    epoch = max(315532800, int(os.environ.get("SOURCE_DATE_EPOCH", "315532800")))
    timestamp = time.gmtime(epoch)[:6]
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for file in sorted(destination.rglob("*")):
            if not file.is_file():
                continue
            info = zipfile.ZipInfo(f"{name}/{file.relative_to(destination).as_posix()}", timestamp)
            info.create_system = 3
            info.external_attr = (0o100755 if os.access(file, os.X_OK) else 0o100644) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, file.read_bytes(), compresslevel=9)
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    checksum_file = archive.with_suffix(".zip.sha256")
    checksum_file.write_text(f"{digest}  {archive.name}\n")
    print(json.dumps({"archive": str(archive), "sha256": digest, "files": len(checksums) + 1,
                      "bytes": archive.stat().st_size}, indent=2))


if __name__ == "__main__":
    main()
