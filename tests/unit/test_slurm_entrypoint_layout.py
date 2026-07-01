import unittest
from pathlib import Path


class SlurmEntrypointLayoutTests(unittest.TestCase):
    def test_supported_deploy_entrypoints_exist(self):
        root = Path(__file__).resolve().parents[2]
        deploy = root / "deploy" / "slurm"
        for relpath in [
            "submit_llama.sh",
            "submit_ollama.sh",
            "connect_claude_llama.sh",
            "run_codex_llama.sh",
            "run_codex_llamacpp_proxy.sh",
            "llama_server.slurm",
            "ollama_server.slurm",
        ]:
            self.assertTrue((deploy / relpath).exists(), relpath)

    def test_root_shims_forward_to_deploy_scripts(self):
        root = Path(__file__).resolve().parents[2]
        expected = {
            "submit_llama.sh": "deploy/slurm/submit_llama.sh",
            "submit_ollama.sh": "deploy/slurm/submit_ollama.sh",
            "connect_claude_llama.sh": "deploy/slurm/connect_claude_llama.sh",
            "run_codex_llama.sh": "deploy/slurm/run_codex_llama.sh",
            "llama_server.slurm": "deploy/slurm/llama_server.slurm",
            "ollama_server.slurm": "deploy/slurm/ollama_server.slurm",
        }
        for name, target in expected.items():
            content = (root / name).read_text()
            self.assertIn(target, content, name)

    def test_primary_readme_prefers_deploy_entrypoints(self):
        root = Path(__file__).resolve().parents[2]
        readme = (root / "README.md").read_text()
        self.assertIn("deploy/slurm/submit_llama.sh", readme)
        self.assertIn("deploy/slurm/connect_claude_llama.sh", readme)
        self.assertIn("compatibility shim", readme)


if __name__ == "__main__":
    unittest.main()
