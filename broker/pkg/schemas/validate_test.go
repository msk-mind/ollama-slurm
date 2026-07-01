package schemas

import (
	"testing"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func TestValidateResultDocumentSummary(t *testing.T) {
	err := ValidateResult("document_summary", "document_summary_v1", types.Result{
		SchemaName:    "document_summary_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"summary": "summary",
			"key_points": []any{
				"point 1",
			},
			"source_metadata": map[string]any{
				"path": "/tmp/doc.txt",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected valid result, got %v", err)
	}
}

func TestValidateResultRejectsSchemaMismatch(t *testing.T) {
	err := ValidateResult("document_summary", "document_summary_v1", types.Result{
		SchemaName:    "placeholder_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"summary": "summary",
		},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateResultLogAnalysis(t *testing.T) {
	err := ValidateResult("log_analysis", "log_analysis_v1", types.Result{
		SchemaName:    "log_analysis_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"summary": "summary",
			"top_findings": []any{
				map[string]any{"code": "TEST_FAILURE"},
			},
			"timeline": []any{
				map[string]any{"phase": "failure"},
			},
			"suggested_next_steps": []any{"Inspect the first error line."},
		},
	})
	if err != nil {
		t.Fatalf("expected valid result, got %v", err)
	}
}

func TestValidateResultRepoSummary(t *testing.T) {
	err := ValidateResult("repo_summary", "repo_summary_v1", types.Result{
		SchemaName:    "repo_summary_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"summary":      "summary",
			"subsystems":   []any{map[string]any{"name": "broker"}},
			"entrypoints":  []any{map[string]any{"path": "cmd/main.go"}},
			"dependencies": []any{map[string]any{"name": "Go"}},
			"risks":        []any{"example risk"},
		},
	})
	if err != nil {
		t.Fatalf("expected valid result, got %v", err)
	}
}

func TestValidateResultRAGEvidencePack(t *testing.T) {
	err := ValidateResult("rag_compress", "rag_evidence_pack_v1", types.Result{
		SchemaName:    "rag_evidence_pack_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"query": "why did it fail",
			"retrieval": map[string]any{
				"strategies": []any{"ripgrep"},
			},
			"retrieval_plan": map[string]any{
				"requested_strategies": []any{"ripgrep"},
				"effective_strategies": []any{"ripgrep"},
			},
			"retrieval_trace": map[string]any{
				"strategy_executions": []any{
					map[string]any{"strategy": "ripgrep", "candidate_count": 1},
				},
			},
			"evidence": []any{
				map[string]any{"id": "ev_001"},
			},
			"budget": map[string]any{
				"retrieved_chunk_tokens": 100,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected valid result, got %v", err)
	}
}

func TestValidateResultPatchProposalPack(t *testing.T) {
	err := ValidateResult("propose_patch", "patch_proposal_pack_v1", types.Result{
		SchemaName:    "patch_proposal_pack_v1",
		SchemaVersion: "1.0.0",
		Payload: map[string]any{
			"summary": "summary",
			"patches": []any{
				map[string]any{"patch_ref": "artifact_patch_plan"},
			},
			"validation_steps": []any{"go test ./..."},
		},
	})
	if err != nil {
		t.Fatalf("expected valid result, got %v", err)
	}
}
