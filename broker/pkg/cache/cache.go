package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

func KeyForRequest(req types.SubmitJobRequest) (string, bool, error) {
	if !isCacheableTask(req.TaskType) {
		return "", false, nil
	}
	if len(req.InputRefs) != 1 {
		return "", false, nil
	}
	input := req.InputRefs[0]
	if input.Type != "file" && input.Type != "directory" && input.Type != "repo" {
		return "", false, nil
	}

	path, err := filePathFromURI(input.URI)
	if err != nil {
		return "", false, err
	}

	contentHash := ""
	switch input.Type {
	case "file":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false, nil
		}
		contentHash = sumBytes(data)
	case "directory", "repo":
		contentHash, err = hashDirectory(path)
		if err != nil {
			return "", false, nil
		}
	}

	payload := map[string]any{
		"task_type":      req.TaskType,
		"schema_name":    req.OutputSchema.Name,
		"input_type":     input.Type,
		"input_uri":      input.URI,
		"content_hash":   contentHash,
		"task_params":    stableTaskParams(req.TaskParams),
		"constraints":    req.Constraints,
		"execution":      stableExecutionProfile(req.ExecutionProfile),
		"classification": input.Classification,
	}

	serialized, err := json.Marshal(payload)
	if err != nil {
		return "", false, err
	}
	return "sha256:" + sumBytes(serialized), true, nil
}

func stableExecutionProfile(profile types.ExecutionProfile) map[string]any {
	return map[string]any{
		"tier":        strings.TrimSpace(profile.Tier),
		"model":       strings.TrimSpace(profile.Model),
		"runtime":     strings.TrimSpace(profile.Runtime),
		"accelerator": strings.TrimSpace(profile.Accelerator),
	}
}

func FindCompletedJobByCacheKey(ctx context.Context, jobStore store.JobStore, cacheKey string) (*types.Job, error) {
	if cacheKey == "" {
		return nil, nil
	}
	jobs, err := jobStore.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.CacheKey == cacheKey && job.State == types.JobStateSucceeded && job.Result != nil {
			candidate := job
			return &candidate, nil
		}
	}
	return nil, nil
}

func isCacheableTask(taskType string) bool {
	switch taskType {
	case "document_summary", "log_analysis", "repo_summary", "rag_compress", "summarize_logs", "inspect_repo", "debug_with_local_context":
		return true
	default:
		return false
	}
}

func filePathFromURI(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported cacheable input uri: %s", uri)
	}
	return parsed.Path, nil
}

func sumBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stableTaskParams(taskParams map[string]any) map[string]any {
	if len(taskParams) == 0 {
		return map[string]any{}
	}
	keys := make([]string, 0, len(taskParams))
	for k := range taskParams {
		if len(k) > 0 && k[0] == '_' {
			continue
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = taskParams[k]
	}
	return out
}

func hashDirectory(root string) (string, error) {
	entries := make([]map[string]any, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if shouldIgnoreDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, map[string]any{
			"path":         rel,
			"content_hash": sumBytes(data),
		})
		return nil
	})
	if err != nil {
		return "", err
	}

	serialized, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return sumBytes(serialized), nil
}

func shouldIgnoreDir(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".broker", "__pycache__", ".pytest_cache", "node_modules":
			return true
		}
	}
	return false
}
