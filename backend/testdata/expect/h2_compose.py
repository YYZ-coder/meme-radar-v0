"""Validate H2-2's public compose/Makefile entry points without deploying.

Independent expectations: docs/handoff/h2-api-pg.md §2-2 and H2-2.
No resolved configuration or environment values are printed.
"""
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[3]


def output(command):
    result = subprocess.run(command, cwd=ROOT, text=True, capture_output=True)
    if result.returncode:
        raise AssertionError(f"{' '.join(command)} failed (exit {result.returncode}); output withheld")
    return result.stdout


def main():
    command = ["docker", "compose", "--env-file", "/dev/null", "--profile", "ingest",
               "config", "--no-env-resolution", "--format", "json"]
    services = json.loads(output(command))["services"]
    assert "api" in services, "ingest profile must include the PostgreSQL api service"
    service = services["api"]
    assert service.get("profiles") == ["ingest"], "api must use the ingest profile"
    assert service["entrypoint"] == ["radar", "api", "--store=pg", "--addr=0.0.0.0:8081"], "api must run PG on container port 8081"
    ports = service.get("ports", [])
    assert len(ports) == 1 and ports[0]["host_ip"] == "127.0.0.1" and ports[0]["target"] == 8081 and str(ports[0]["published"]) == "8081", "api must publish only 127.0.0.1:8081"
    assert any(Path(item["path"]).name == ".env" for item in service.get("env_file", [])), "api must read its credentials from .env"
    assert "RADAR_ADMIN_TOKEN" not in service.get("environment", {}), "api must not override .env with a default admin token"
    health = service.get("healthcheck", {})
    assert not health.get("disable", False), "api readiness check must be enabled"
    assert any("127.0.0.1:8081/healthz" in item for item in health.get("test", [])), "api readiness must probe port 8081"
    for target, action in [("ingest-up", "up"), ("ingest-down", "stop"), ("ingest-logs", "logs")]:
        commands = output(["make", "-n", target]).splitlines()
        compose = [shlex.split(line) for line in commands if line.startswith("docker compose ")]
        assert any(action in argv and "api" in argv for argv in compose), f"make {target} must include api"
    print("PASS: H2-2 compose profile, PG entrypoint, loopback port, env file, readiness and Makefile lifecycle")


if __name__ == "__main__":
    main()
