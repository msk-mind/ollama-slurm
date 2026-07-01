package policy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/limr/ollama-slurm/broker/pkg/types"
)

var ErrPolicyDenied = errors.New("policy denied")

var sensitiveClassifications = map[string]struct{}{
	"restricted":      {},
	"phi":             {},
	"secret_adjacent": {},
}

func AuthorizeJobLogs(job types.Job) error {
	if allowLogReleaseOverride(job.Request.TaskParams) {
		return nil
	}
	if strings.EqualFold(job.Request.Constraints.Confidentiality, "local_only") {
		return fmt.Errorf("%w: log release disabled for confidentiality=local_only", ErrPolicyDenied)
	}
	for _, input := range job.Request.InputRefs {
		classification := strings.ToLower(strings.TrimSpace(input.Classification))
		if _, denied := sensitiveClassifications[classification]; denied {
			return fmt.Errorf("%w: log release disabled for classification=%s", ErrPolicyDenied, classification)
		}
	}
	return nil
}

func allowLogReleaseOverride(taskParams map[string]any) bool {
	if len(taskParams) == 0 {
		return false
	}
	raw, ok := taskParams["allow_log_release"]
	if !ok {
		return false
	}
	allowed, ok := raw.(bool)
	return ok && allowed
}

func FilterJobResult(job types.Job) (*types.Result, []types.Artifact, error) {
	if job.Result == nil {
		return nil, nil, nil
	}

	result := cloneResult(*job.Result)
	artifacts := cloneArtifacts(job.Artifacts)
	if !requiresSensitiveReleaseFiltering(job) {
		return &result, artifacts, nil
	}

	result.Payload = redactPayload(result.Payload)
	appendWarning(result.Payload, "broker_redacted_sensitive_fields")

	if !allowArtifactReleaseOverride(job.Request.TaskParams) {
		appendWarning(result.Payload, "broker_withheld_artifacts")
		return &result, nil, nil
	}

	for i := range artifacts {
		artifacts[i].Path = ""
	}
	appendWarning(result.Payload, "broker_removed_artifact_paths")
	return &result, artifacts, nil
}

func requiresSensitiveReleaseFiltering(job types.Job) bool {
	if strings.EqualFold(job.Request.Constraints.Confidentiality, "local_only") {
		return true
	}
	for _, input := range job.Request.InputRefs {
		classification := strings.ToLower(strings.TrimSpace(input.Classification))
		if _, denied := sensitiveClassifications[classification]; denied {
			return true
		}
	}
	return false
}

func allowArtifactReleaseOverride(taskParams map[string]any) bool {
	if len(taskParams) == 0 {
		return false
	}
	raw, ok := taskParams["allow_artifact_release"]
	if !ok {
		return false
	}
	allowed, ok := raw.(bool)
	return ok && allowed
}

func cloneResult(in types.Result) types.Result {
	return types.Result{
		SchemaName:    in.SchemaName,
		SchemaVersion: in.SchemaVersion,
		Payload:       cloneMap(in.Payload),
	}
}

func cloneArtifacts(in []types.Artifact) []types.Artifact {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Artifact, len(in))
	copy(out, in)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneValue(value)
	}
	return out
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return typed
	}
}

func redactPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "path":
			out[key] = "[REDACTED]"
		case "paths", "related_paths":
			out[key] = redactStringSlice(value)
		default:
			out[key] = redactValue(lowerKey, value)
		}
	}
	return out
}

func redactValue(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactPayload(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			if itemMap, ok := item.(map[string]any); ok {
				out[i] = redactPayload(itemMap)
				continue
			}
			if isPathListKey(key) {
				if _, ok := item.(string); ok {
					out[i] = "[REDACTED]"
					continue
				}
			}
			out[i] = cloneValue(item)
		}
		return out
	default:
		if isPathFieldKey(key) {
			if _, ok := typed.(string); ok {
				return "[REDACTED]"
			}
		}
		return typed
	}
}

func redactStringSlice(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return []any{"[REDACTED]"}
	}
	out := make([]any, len(items))
	for i := range items {
		out[i] = "[REDACTED]"
	}
	return out
}

func isPathFieldKey(key string) bool {
	return key == "path"
}

func isPathListKey(key string) bool {
	return key == "paths" || key == "related_paths"
}

func appendWarning(payload map[string]any, warning string) {
	if payload == nil {
		return
	}
	existing, _ := payload["warnings"].([]any)
	for _, item := range existing {
		if text, ok := item.(string); ok && text == warning {
			payload["warnings"] = existing
			return
		}
	}
	payload["warnings"] = append(existing, warning)
}
