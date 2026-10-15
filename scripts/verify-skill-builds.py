#!/usr/bin/env python3
"""Cross-build and check embedded canonical bytes without installing anything."""
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
payload = {p.relative_to(root / "skills" / "agent-debug-squad"): p.read_bytes()
           for p in (root / "skills" / "agent-debug-squad").rglob("*") if p.is_file()}
with tempfile.TemporaryDirectory(prefix="squad-skill-builds-") as output:
    for target_os in ("darwin", "linux"):
        for arch in ("amd64", "arm64"):
            binary = Path(output) / f"agent-debug-squad_{target_os}_{arch}"
            env = dict(os.environ, CGO_ENABLED="0", GOOS=target_os, GOARCH=arch)
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/agent-debug-squad"],
                           cwd=root, env=env, check=True)
            contents = binary.read_bytes()
            for name, data in payload.items():
                if data not in contents:
                    raise SystemExit(f"Missing embedded bytes in {binary.name}: {name}")
            print(f"Verified {binary.name}: {len(payload)} canonical resources", flush=True)
