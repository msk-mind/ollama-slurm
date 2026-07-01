import importlib.util
import unittest
from pathlib import Path


def load_proxy_module():
    module_path = Path(__file__).resolve().parents[2] / "deploy" / "slurm" / "codex_llamacpp_proxy.py"
    spec = importlib.util.spec_from_file_location("codex_llamacpp_proxy", module_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Failed to load proxy module from {module_path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


proxy = load_proxy_module()


class NormalizeModelsResponseTests(unittest.TestCase):
    def test_normalizes_llamacpp_catalog_and_aliases(self):
        payload = {
            "data": [
                {
                    "id": "gpt-oss-20b-mxfp4.gguf",
                    "owned_by": "llamacpp",
                }
            ]
        }

        result = proxy.normalize_models_response(payload, ["gpt-oss-20b"])

        self.assertIn("models", result)
        slugs = [item["slug"] for item in result["models"]]
        self.assertEqual(
            slugs,
            ["gpt-oss-20b-mxfp4.gguf", "gpt-oss-20b-mxfp4", "gpt-oss-20b"],
        )
        self.assertEqual(result["models"][0]["truncation_policy"], {"mode": "tokens", "limit": 65536})
        self.assertEqual(result["models"][0]["supported_reasoning_levels"][1]["effort"], "medium")

    def test_deduplicates_aliases(self):
        payload = {
            "data": [
                {
                    "id": "gpt-oss-20b.gguf",
                    "owned_by": "llamacpp",
                }
            ]
        }

        result = proxy.normalize_models_response(payload, ["gpt-oss-20b", "gpt-oss-20b"])

        slugs = [item["slug"] for item in result["models"]]
        self.assertEqual(slugs, ["gpt-oss-20b.gguf", "gpt-oss-20b"])


class RewriteResponsesRequestTests(unittest.TestCase):
    def test_rewrites_non_function_tools(self):
        payload = {
            "tools": [
                {"type": "web_search"},
                {"type": "function", "name": "already_ok", "parameters": {"type": "object"}},
                {"type": "image_generation", "name": "image_generation", "description": "make image"},
            ]
        }

        result = proxy.rewrite_responses_request(payload)
        tools = result["tools"]

        self.assertEqual(tools[0]["type"], "function")
        self.assertEqual(tools[0]["name"], "web_search")
        self.assertIn("parameters", tools[0])
        self.assertEqual(tools[1]["type"], "function")
        self.assertEqual(tools[1]["name"], "already_ok")
        self.assertEqual(tools[2]["type"], "function")
        self.assertEqual(tools[2]["name"], "image_generation")


if __name__ == "__main__":
    unittest.main()
