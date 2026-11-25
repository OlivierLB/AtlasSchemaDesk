#!/usr/bin/env python3
"""Noninteractive adapters for legacy Docker restoration commands.

Only the legacy `sudo rm -rf old` cleanup is replaced, and only after checking
that its directory belongs to the configured MySQL container's data mount.
Other sudo invocations retain Sulcata's native askpass authentication.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from contextlib import contextmanager


def docker_arguments(arguments):
    """Remove pseudo-TTY allocation, preserving stdin and client arguments."""
    if not arguments or arguments[0] not in ("run", "exec"):
        return arguments
    result = [arguments[0]]
    values = {
        "-e", "--env", "--env-file", "-v", "--volume", "--mount", "-w",
        "--workdir", "-u", "--user", "--name", "--network", "--entrypoint",
        "-p", "--publish", "--hostname", "--label", "-l", "--platform",
        "--pull", "--device", "--add-host", "--restart", "--detach-keys",
    }
    index = 1
    while index < len(arguments):
        argument = arguments[index]
        if not argument.startswith("-") or argument == "--":
            return result + arguments[index:]
        if argument in ("-t", "--tty", "--tty=true"):
            index += 1
            continue
        if argument in ("-it", "-ti"):
            argument = "-i"
        result.append(argument)
        if argument in values and index + 1 < len(arguments):
            index += 1
            result.append(arguments[index])
        index += 1
    return result


def cleanup_old(docker, container, cwd):
    """Use the existing local MySQL image to clean its retired data directory."""
    mounts = json.loads(subprocess.check_output(
        [docker, "inspect", "--format", "{{json .Mounts}}", container], text=True,
    ))
    data = next((m for m in mounts if m["Destination"] == "/var/lib/mysql"), None)
    cwd = Path(cwd).resolve()
    if not data or Path(data["Source"]).resolve() != cwd / "current":
        return False
    old = cwd / "old"
    if old.is_symlink() or not old.is_dir():
        return False
    image = subprocess.check_output(
        [docker, "inspect", "--format", "{{.Image}}", container], text=True,
    ).strip()
    if not image.startswith("sha256:"):
        return False
    print("Nettoyage de l’ancien volume MySQL via Docker, sans sudo.", flush=True)
    subprocess.run([
        docker, "run", "--rm", "--pull=never", "--network=none", "--read-only",
        "--user=0", "--entrypoint=/bin/sh", "--mount",
        f"type=bind,src={old},dst=/cleanup", image, "-c",
        "find /cleanup -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +",
    ], check=True)
    old.rmdir()
    return True


@contextmanager
def environment(target):
    env = os.environ.copy()
    docker = shutil.which("docker")
    sudo = shutil.which("sudo")
    with tempfile.TemporaryDirectory(prefix="sulcata-restore-") as directory:
        env["SULCATA_REAL_DOCKER"] = docker or ""
        env["SULCATA_FALLBACK_SUDO"] = sudo or ""
        env["SULCATA_MYSQL_CLEANUP_CONTAINER"] = target.get("cleanupContainer", "")
        import shlex
        for name in ("docker", "sudo"):
            script = Path(directory) / name
            script.write_text(
                "#!/bin/sh\nexec " + shlex.quote(sys.executable) + " "
                + shlex.quote(str(Path(__file__).resolve())) + " " + name + ' "$@"\n'
            )
            script.chmod(0o700)
        env["PATH"] = directory + os.pathsep + env.get("PATH", "")
        env["PYTHONUNBUFFERED"] = "1"
        yield env


def main():
    name, arguments = sys.argv[1], sys.argv[2:]
    docker = os.environ.get("SULCATA_REAL_DOCKER", "")
    if name == "docker":
        if not docker:
            raise SystemExit("Docker est introuvable. Vérifie son installation.")
        os.execv(docker, [docker] + docker_arguments(arguments))
    if name != "sudo":
        raise SystemExit("Adaptateur de restauration inconnu.")
    container = os.environ.get("SULCATA_MYSQL_CLEANUP_CONTAINER", "")
    if container and docker and arguments == ["rm", "-rf", "old"]:
        try:
            if cleanup_old(docker, container, Path.cwd()):
                return
        except (subprocess.CalledProcessError, OSError, ValueError):
            print("Le nettoyage sans sudo n’a pas abouti ; demande d’autorisation système.", flush=True)
    sudo = os.environ.get("SULCATA_FALLBACK_SUDO", "")
    if not sudo:
        raise SystemExit("Cette opération nécessite sudo, qui est introuvable.")
    os.execv(sudo, [sudo] + arguments)


if __name__ == "__main__":
    main()
