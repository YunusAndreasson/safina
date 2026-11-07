#!/usr/bin/env python3
"""Build the pinned evaluation library with a local verified toolchain.

Linux x86-64 only. Nothing is installed globally. Cold builds may take minutes.
"""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import tarfile
import urllib.request

root = Path(__file__).resolve().parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--cache", type=Path, default=root / ".build")
args = parser.parse_args()
if platform.system() != "Linux" or platform.machine() != "x86_64":
    parser.error("this pinned toolchain is Linux x86-64; build libghostty-vt separately on other platforms")
versions = json.loads((root / "versions.json").read_text())
cache = args.cache.resolve()
cache.mkdir(parents=True, exist_ok=True)
for name, url_key, hash_key, directory in [
    ("zig.tar.xz", "zig_url", "zig_sha256", "zig-x86_64-linux-" + versions["zig"]),
    ("ghostty.tar.gz", "source_url", "source_sha256", "ghostty-" + versions["ghostty_commit"]),
]:
    archive = cache / name
    if not archive.exists():
        print("Downloading " + versions[url_key], flush=True)
        temporary = archive.with_suffix(archive.suffix + ".part")
        urllib.request.urlretrieve(versions[url_key], temporary)
        temporary.replace(archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != versions[hash_key]:
        raise SystemExit(f"Checksum mismatch: {archive}; remove this archive before retrying")
    if not (cache / directory).exists():
        with tarfile.open(archive) as source:
            source.extractall(cache, filter="data")
zig = cache / ("zig-x86_64-linux-" + versions["zig"]) / "zig"
prefix = cache / "native"
subprocess.run([str(zig), "build", *versions["native_flags"], "--prefix", str(prefix)],
               cwd=cache / ("ghostty-" + versions["ghostty_commit"]), check=True)
print("PKG_CONFIG_PATH=" + str(prefix / "lib" / "pkgconfig"))
