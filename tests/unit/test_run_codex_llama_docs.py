import unittest
from pathlib import Path


class RunCodexLlamaDocsTests(unittest.TestCase):
    def test_supported_runner_exists(self):
        root = Path(__file__).resolve().parents[2]
        self.assertTrue((root / "run_codex_llama.sh").exists())
        self.assertTrue((root / "deploy" / "slurm" / "run_codex_llama.sh").exists())
        self.assertTrue((root / "deploy" / "slurm" / "run_codex_llamacpp_proxy.sh").exists())

    def test_readme_mentions_runner(self):
        root = Path(__file__).resolve().parents[2]
        readme = (root / "README.md").read_text()
        self.assertIn("deploy/slurm/run_codex_llama.sh", readme)
        self.assertIn("Run Codex End To End", readme)


if __name__ == "__main__":
    unittest.main()
