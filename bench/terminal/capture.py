#!/usr/bin/env python3
"""Capture only synthetic vim/less output in an 80x24 PTY for offline replay."""
import fcntl
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import termios
import time

root = Path(__file__).resolve().parent
content = root / "testdata" / "content.txt"
content.write_text("Safina terminal fixture\nUnicode: 界 é 👩‍💻\n" + "".join(f"line {i:03d}: synthetic log data\n" for i in range(1, 100)))
for name, command, quit_bytes in [
    ("vim", ["vim", "-Nu", "NONE", "-n", "-i", "NONE", "-c", "set noshowmode noruler", content.name], b"\x1b:qa!\r"),
    ("less", ["less", "-R", "--", content.name], b"q"),
]:
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    env = {"PATH": os.environ["PATH"], "TERM": "xterm-256color", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "HOME": "/nonexistent"}
    process = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave, env=env, cwd=content.parent, start_new_session=True)
    os.close(slave)
    output = bytearray()
    try:
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                chunk = os.read(master, 65536)
                output.extend(chunk)
                if b"\x1b[6n" in chunk:
                    os.write(master, b"\x1b[1;1R")
        (root / "testdata" / f"{name}.vt").write_bytes(output)
        os.write(master, quit_bytes)
        process.wait(timeout=5)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        os.close(master)
    print(f"{name}: {len(output)} bytes")
