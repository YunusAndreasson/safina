#!/usr/bin/env python3
"""Build the pinned terminal library with a local verified toolchain.

Linux and macOS, x86-64 and ARM64. Nothing is installed globally.
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
parser.add_argument("--cache", type=Path, default=root.parent / ".build" / "libghostty")
args = parser.parse_args()
versions = json.loads((root / "libghostty.json").read_text())
arch = {"x86_64": "x86_64", "AMD64": "x86_64", "aarch64": "aarch64", "arm64": "aarch64"}.get(platform.machine())
os_name = {"Linux": "linux", "Darwin": "macos"}.get(platform.system())
target = f"{arch}-{os_name}"
if target not in versions["toolchains"]:
    parser.error(f"unsupported native build host: {platform.system()} {platform.machine()}")
toolchain = versions["toolchains"][target]
zig_directory = f"zig-{target}-{versions['zig']}"
cache = args.cache.resolve()
cache.mkdir(parents=True, exist_ok=True)
for name, url, expected_hash, directory in [
    ("zig.tar.xz", toolchain["url"], toolchain["sha256"], zig_directory),
    ("ghostty.tar.gz", versions["source_url"], versions["source_sha256"], "ghostty-" + versions["ghostty_commit"]),
]:
    archive = cache / name
    if not archive.exists():
        print("Downloading " + url, flush=True)
        temporary = archive.with_suffix(archive.suffix + ".part")
        urllib.request.urlretrieve(url, temporary)
        temporary.replace(archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != expected_hash:
        raise SystemExit(f"Checksum mismatch: {archive}; remove this archive before retrying")
    if not (cache / directory).exists():
        with tarfile.open(archive) as source:
            source.extractall(cache, filter="data")
zig = cache / zig_directory / "zig"
prefix = cache / "native"
subprocess.run([str(zig), "build", *versions["native_flags"], "--prefix", str(prefix)],
               cwd=cache / ("ghostty-" + versions["ghostty_commit"]), check=True)
print("PKG_CONFIG_PATH=" + str(prefix / "lib" / "pkgconfig"))
