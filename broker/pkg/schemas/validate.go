package schemas

import (
	"fmt"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func ValidateResult(taskType, expectedSchema string, result types.Result) error {
	if expectedSchema == "" {
		return fmt.Errorf("expected schema is required")
	}
	if result.SchemaName != expectedSchema {
		return fmt.Errorf("schema name mismatch: expected %q, got %q", expectedSchema, result.SchemaName)
	}
	if result.SchemaVersion == "" {
		return fmt.Errorf("schema version is required")
	}
	if result.Payload == nil {
		return fmt.Errorf("payload is required")
	}

	switch expectedSchema {
	case "document_summary_v1":
		return validateDocumentSummary(taskType, result.Payload)
	case "log_analysis_v1":
		return validateLogAnalysis(taskType, result.Payload)
	case "repo_summary_v1":
		return validateRepoSummary(taskType, result.Payload)
	case "rag_evidence_pack_v1":
		return validateRAGEvidencePack(taskType, result.Payload)
	case "debug_evidence_pack_v1":
		return validateDebugEvidencePack(taskType, result.Payload)
	case "log_evidence_pack_v1":
		return validateLogEvidencePack(taskType, result.Payload)
	case "repo_inspection_pack_v1":
		return validateRepoInspectionPack(taskType, result.Payload)
	case "patch_proposal_pack_v1":
		return validatePatchProposalPack(taskType, result.Payload)
	default:
		if _, ok := result.Payload["summary"].(string); !ok {
			return fmt.Errorf("payload.summary must be a string")
		}
		return nil
	}
}

func validateDocumentSummary(taskType string, payload map[string]any) error {
	if taskType != "document_summary" {
		return fmt.Errorf("schema document_summary_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["summary"].(string); !ok {
		return fmt.Errorf("payload.summary must be a string")
	}
	if keyPoints, exists := payload["key_points"]; exists {
		if _, ok := keyPoints.([]any); !ok {
			return fmt.Errorf("payload.key_points must be an array")
		}
	}
	if sourceMetadata, exists := payload["source_metadata"]; exists {
		if _, ok := sourceMetadata.(map[string]any); !ok {
			return fmt.Errorf("payload.source_metadata must be an object")
		}
	}
	return nil
}

func validateLogAnalysis(taskType string, payload map[string]any) error {
	if taskType != "log_analysis" {
		return fmt.Errorf("schema log_analysis_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["summary"].(string); !ok {
		return fmt.Errorf("payload.summary must be a string")
	}
	if findings, exists := payload["top_findings"]; exists {
		if _, ok := findings.([]any); !ok {
			return fmt.Errorf("payload.top_findings must be an array")
		}
	}
	if timeline, exists := payload["timeline"]; exists {
		if _, ok := timeline.([]any); !ok {
			return fmt.Errorf("payload.timeline must be an array")
		}
	}
	if nextSteps, exists := payload["suggested_next_steps"]; exists {
		if _, ok := nextSteps.([]any); !ok {
			return fmt.Errorf("payload.suggested_next_steps must be an array")
		}
	}
	return nil
}

func validateRepoSummary(taskType string, payload map[string]any) error {
	if taskType != "repo_summary" {
		return fmt.Errorf("schema repo_summary_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["summary"].(string); !ok {
		return fmt.Errorf("payload.summary must be a string")
	}
	if subsystems, exists := payload["subsystems"]; exists {
		if _, ok := subsystems.([]any); !ok {
			return fmt.Errorf("payload.subsystems must be an array")
		}
	}
	if entrypoints, exists := payload["entrypoints"]; exists {
		if _, ok := entrypoints.([]any); !ok {
			return fmt.Errorf("payload.entrypoints must be an array")
		}
	}
	if dependencies, exists := payload["dependencies"]; exists {
		if _, ok := dependencies.([]any); !ok {
			return fmt.Errorf("payload.dependencies must be an array")
		}
	}
	if risks, exists := payload["risks"]; exists {
		if _, ok := risks.([]any); !ok {
			return fmt.Errorf("payload.risks must be an array")
		}
	}
	return nil
}

func validateRAGEvidencePack(taskType string, payload map[string]any) error {
	if taskType != "rag_compress" {
		return fmt.Errorf("schema rag_evidence_pack_v1 is incompatible with task type %q", taskType)
	}
	return validateEvidencePackShape(payload, true)
}

func validateDebugEvidencePack(taskType string, payload map[string]any) error {
	if taskType != "debug_with_local_context" {
		return fmt.Errorf("schema debug_evidence_pack_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["problem"].(string); !ok {
		return fmt.Errorf("payload.problem must be a string")
	}
	if hypotheses, exists := payload["top_hypotheses"]; exists {
		if _, ok := hypotheses.([]any); !ok {
			return fmt.Errorf("payload.top_hypotheses must be an array")
		}
	}
	if evidence, exists := payload["evidence"]; exists {
		if _, ok := evidence.([]any); !ok {
			return fmt.Errorf("payload.evidence must be an array")
		}
	}
	return nil
}

func validateLogEvidencePack(taskType string, payload map[string]any) error {
	if taskType != "summarize_logs" {
		return fmt.Errorf("schema log_evidence_pack_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["summary"].(string); !ok {
		return fmt.Errorf("payload.summary must be a string")
	}
	if timeline, exists := payload["timeline"]; exists {
		if _, ok := timeline.([]any); !ok {
			return fmt.Errorf("payload.timeline must be an array")
		}
	}
	if clusters, exists := payload["clusters"]; exists {
		if _, ok := clusters.([]any); !ok {
			return fmt.Errorf("payload.clusters must be an array")
		}
	}
	if evidence, exists := payload["evidence"]; exists {
		if _, ok := evidence.([]any); !ok {
			return fmt.Errorf("payload.evidence must be an array")
		}
	}
	return nil
}

func validateRepoInspectionPack(taskType string, payload map[string]any) error {
	if taskType != "inspect_repo" {
		return fmt.Errorf("schema repo_inspection_pack_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["query"].(string); !ok {
		return fmt.Errorf("payload.query must be a string")
	}
	if subsystems, exists := payload["subsystems"]; exists {
		if _, ok := subsystems.([]any); !ok {
			return fmt.Errorf("payload.subsystems must be an array")
		}
	}
	if symbols, exists := payload["symbols"]; exists {
		if _, ok := symbols.([]any); !ok {
			return fmt.Errorf("payload.symbols must be an array")
		}
	}
	if evidence, exists := payload["evidence"]; exists {
		if _, ok := evidence.([]any); !ok {
			return fmt.Errorf("payload.evidence must be an array")
		}
	}
	return nil
}

func validatePatchProposalPack(taskType string, payload map[string]any) error {
	if taskType != "propose_patch" {
		return fmt.Errorf("schema patch_proposal_pack_v1 is incompatible with task type %q", taskType)
	}
	if _, ok := payload["summary"].(string); !ok {
		return fmt.Errorf("payload.summary must be a string")
	}
	if patches, exists := payload["patches"]; exists {
		if _, ok := patches.([]any); !ok {
			return fmt.Errorf("payload.patches must be an array")
		}
	}
	if validationSteps, exists := payload["validation_steps"]; exists {
		if _, ok := validationSteps.([]any); !ok {
			return fmt.Errorf("payload.validation_steps must be an array")
		}
	}
	return nil
}

func validateEvidencePackShape(payload map[string]any, requireQuery bool) error {
	if requireQuery {
		if _, ok := payload["query"].(string); !ok {
			return fmt.Errorf("payload.query must be a string")
		}
	}
	if retrieval, exists := payload["retrieval"]; exists {
		if _, ok := retrieval.(map[string]any); !ok {
			return fmt.Errorf("payload.retrieval must be an object")
		}
	}
	if retrievalPlan, exists := payload["retrieval_plan"]; exists {
		if _, ok := retrievalPlan.(map[string]any); !ok {
			return fmt.Errorf("payload.retrieval_plan must be an object")
		}
	}
	if retrievalTrace, exists := payload["retrieval_trace"]; exists {
		if _, ok := retrievalTrace.(map[string]any); !ok {
			return fmt.Errorf("payload.retrieval_trace must be an object")
		}
	}
	if evidence, exists := payload["evidence"]; exists {
		if _, ok := evidence.([]any); !ok {
			return fmt.Errorf("payload.evidence must be an array")
		}
	}
	if budget, exists := payload["budget"]; exists {
		if _, ok := budget.(map[string]any); !ok {
			return fmt.Errorf("payload.budget must be an object")
		}
	}
	return nil
}
