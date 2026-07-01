#!/usr/bin/env python3

import importlib.util
from pathlib import Path


def main() -> int:
    target = Path(__file__).resolve().parents[2] / "deploy" / "slurm" / "codex_llamacpp_proxy.py"
    spec = importlib.util.spec_from_file_location("codex_llamacpp_proxy", target)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Failed to load proxy module from {target}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.main()


if __name__ == "__main__":
    raise SystemExit(main())
