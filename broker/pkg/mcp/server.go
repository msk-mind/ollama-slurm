package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

const protocolVersion = "2025-11-25"

type Server struct {
	service          *service.Service
	defaultPrincipal auth.Principal
	mu               sync.RWMutex
	sessionPrincipal auth.Principal
}

type messageFraming int

const (
	framingContentLength messageFraming = iota
	framingNDJSON
)

func NewServer(svc *service.Service, defaultPrincipal auth.Principal) *Server {
	return &Server{
		service:          svc,
		defaultPrincipal: defaultPrincipal,
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string     `json:"jsonrpc"`
	ID      any        `json:"id,omitempty"`
	Result  any        `json:"result,omitempty"`
	Error   *respError `json:"error,omitempty"`
}

type respError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type initializeParams struct {
	Auth *struct {
		Actor string `json:"actor"`
		Role  string `json:"role"`
	} `json:"auth,omitempty"`
}

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	defer writer.Flush()

	for {
		payload, framing, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req request
		if err := json.Unmarshal(payload, &req); err != nil {
			if err := writeMessage(writer, response{
				JSONRPC: "2.0",
				Error:   &respError{Code: -32700, Message: "parse error"},
			}, framing); err != nil {
				return err
			}
			continue
		}

		resp := s.handleRequest(ctx, req)
		if req.ID == nil {
			continue
		}
		if err := writeMessage(writer, resp, framing); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req request) response {
	resp := response{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		if _, err := s.initializePrincipal(ctx, req.Params); err != nil {
			resp.Error = &respError{Code: -32001, Message: err.Error()}
			return resp
		}
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo": map[string]any{
				"name":    "local-ai-compute-broker",
				"version": "0.1.0",
			},
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		_, err := s.contextWithPrincipal(ctx)
		if err != nil {
			resp.Error = &respError{Code: -32001, Message: err.Error()}
			return resp
		}
		resp.Result = map[string]any{
			"tools": toolDefinitions(),
		}
	case "tools/call":
		var err error
		ctx, err = s.contextWithPrincipal(ctx)
		if err != nil {
			resp.Error = &respError{Code: -32001, Message: err.Error()}
			return resp
		}
		var params toolsCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &respError{Code: -32602, Message: "invalid tools/call params"}
			return resp
		}
		result, err := s.callTool(ctx, params)
		if err != nil {
			resp.Error = &respError{Code: -32000, Message: err.Error()}
			return resp
		}
		resp.Result = result
	default:
		resp.Error = &respError{Code: -32601, Message: "method not found"}
	}

	return resp
}

func (s *Server) initializePrincipal(ctx context.Context, params json.RawMessage) (auth.Principal, error) {
	if principal := auth.PrincipalFromContext(ctx); principal.Actor != "" {
		s.setSessionPrincipal(principal)
		return principal, nil
	}

	var initParams initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &initParams); err != nil {
			return auth.Principal{}, fmt.Errorf("invalid initialize params")
		}
	}
	if initParams.Auth != nil && strings.TrimSpace(initParams.Auth.Actor) != "" {
		principal := auth.Principal{
			Actor: strings.TrimSpace(initParams.Auth.Actor),
			Role:  defaultRole(initParams.Auth.Role),
		}
		s.setSessionPrincipal(principal)
		return principal, nil
	}
	if s.defaultPrincipal.Actor != "" {
		s.setSessionPrincipal(s.defaultPrincipal)
		return s.defaultPrincipal, nil
	}
	return auth.Principal{}, errors.New("mcp session identity is required; provide initialize.params.auth or BROKER_MCP_ACTOR")
}

func (s *Server) contextWithPrincipal(ctx context.Context) (context.Context, error) {
	if principal := auth.PrincipalFromContext(ctx); principal.Actor != "" {
		return ctx, nil
	}
	s.mu.RLock()
	principal := s.sessionPrincipal
	s.mu.RUnlock()
	if principal.Actor == "" && s.defaultPrincipal.Actor != "" {
		principal = s.defaultPrincipal
	}
	if principal.Actor == "" {
		return nil, errors.New("mcp session is not initialized with an identity")
	}
	return auth.WithPrincipal(ctx, principal), nil
}

func (s *Server) setSessionPrincipal(principal auth.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionPrincipal = principal
}

func defaultRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "user"
	}
	return role
}

func (s *Server) callTool(ctx context.Context, params toolsCallParams) (map[string]any, error) {
	switch params.Name {
	case "submit_local_job":
		var req types.SubmitJobRequest
		if err := json.Unmarshal(params.Arguments, &req); err != nil {
			return nil, fmt.Errorf("invalid submit_local_job arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "submit_parallel_jobs":
		var req types.SubmitParallelJobsRequest
		if err := json.Unmarshal(params.Arguments, &req); err != nil {
			return nil, fmt.Errorf("invalid submit_parallel_jobs arguments")
		}
		resp, err := s.service.SubmitParallelJobs(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "get_root_job_status":
		var args struct {
			RootJobID string `json:"root_job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.RootJobID == "" {
			return nil, fmt.Errorf("invalid get_root_job_status arguments")
		}
		status, err := s.service.GetRootJobStatus(ctx, args.RootJobID)
		if err != nil {
			return nil, err
		}
		return toolResult(status), nil
	case "retry_failed_root_shards":
		var req types.RetryFailedRootShardsRequest
		if err := json.Unmarshal(params.Arguments, &req); err != nil || strings.TrimSpace(req.RootJobID) == "" {
			return nil, fmt.Errorf("invalid retry_failed_root_shards arguments")
		}
		resp, err := s.service.RetryFailedRootShards(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "rag_compress":
		req, err := decodeRAGToolSubmitRequest(params.Arguments, "rag_compress", "rag_evidence_pack_v1")
		if err != nil {
			return nil, fmt.Errorf("invalid rag_compress arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "debug_with_local_context":
		req, err := decodeRAGToolSubmitRequest(params.Arguments, "debug_with_local_context", "debug_evidence_pack_v1")
		if err != nil {
			return nil, fmt.Errorf("invalid debug_with_local_context arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "summarize_logs":
		req, err := decodeRAGToolSubmitRequest(params.Arguments, "summarize_logs", "log_evidence_pack_v1")
		if err != nil {
			return nil, fmt.Errorf("invalid summarize_logs arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "inspect_repo":
		req, err := decodeRAGToolSubmitRequest(params.Arguments, "inspect_repo", "repo_inspection_pack_v1")
		if err != nil {
			return nil, fmt.Errorf("invalid inspect_repo arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "propose_patch":
		req, err := decodeRAGToolSubmitRequest(params.Arguments, "propose_patch", "patch_proposal_pack_v1")
		if err != nil {
			return nil, fmt.Errorf("invalid propose_patch arguments")
		}
		resp, err := s.service.SubmitJob(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "release_deferred_root_chunks":
		var req types.ReleaseDeferredRootChunksRequest
		if err := json.Unmarshal(params.Arguments, &req); err != nil || strings.TrimSpace(req.RootJobID) == "" {
			return nil, fmt.Errorf("invalid release_deferred_root_chunks arguments")
		}
		resp, err := s.service.ReleaseDeferredRootChunks(ctx, req)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "get_job_status":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid get_job_status arguments")
		}
		job, err := s.service.GetJob(ctx, args.JobID)
		if err != nil {
			return nil, err
		}
		return toolResult(job), nil
	case "fetch_result":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid fetch_result arguments")
		}
		release, err := s.service.GetReleasedResult(ctx, args.JobID)
		if err != nil {
			return nil, err
		}
		return toolResult(release), nil
	case "get_retry_recommendation":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid get_retry_recommendation arguments")
		}
		rec, err := s.service.GetJobRetryRecommendation(ctx, args.JobID)
		if err != nil {
			return nil, err
		}
		return toolResult(rec), nil
	case "retry_with_recommended_profile":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid retry_with_recommended_profile arguments")
		}
		resp, err := s.service.RetryJobWithRecommendation(ctx, args.JobID)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "fetch_job_logs":
		var args struct {
			JobID    string `json:"job_id"`
			Stream   string `json:"stream"`
			MaxBytes int    `json:"max_bytes"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid fetch_job_logs arguments")
		}
		logs, err := s.service.GetJobLogs(ctx, args.JobID, args.Stream, args.MaxBytes)
		if err != nil {
			return nil, err
		}
		return toolResult(logs), nil
	case "cancel_job":
		var args struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(params.Arguments, &args); err != nil || args.JobID == "" {
			return nil, fmt.Errorf("invalid cancel_job arguments")
		}
		resp, err := s.service.CancelJob(ctx, args.JobID)
		if err != nil {
			return nil, err
		}
		return toolResult(resp), nil
	case "list_local_capabilities":
		return toolResult(capabilitiesPayload()), nil
	default:
		return nil, fmt.Errorf("unknown tool: %s", params.Name)
	}
}

func toolResult(payload any) map[string]any {
	textBytes, _ := json.MarshalIndent(payload, "", "  ")
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(textBytes),
			},
		},
		"structuredContent": payload,
	}
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		ragToolDefinition("rag_compress", "Compress authorized local inputs into a compact evidence pack for remote synthesis without exporting raw data by default.", []string{"query", "input_refs"}, map[string]any{
			"query":                map[string]any{"type": "string"},
			"input_refs":           inputRefsSchema(),
			"retrieval_strategies": retrievalStrategiesSchema(),
			"task_params":          map[string]any{"type": "object"},
			"constraints":          ragConstraintsSchema(),
			"execution_profile":    ragExecutionProfileSchema(),
			"idempotency_key":      map[string]any{"type": "string"},
		}),
		ragToolDefinition("debug_with_local_context", "Run a local debugging RAG workflow over logs, stack traces, tests, repo paths, and git history.", []string{"problem", "input_refs"}, map[string]any{
			"problem":              map[string]any{"type": "string"},
			"input_refs":           inputRefsSchema(),
			"retrieval_strategies": retrievalStrategiesSchema(),
			"task_params":          map[string]any{"type": "object"},
			"constraints":          ragConstraintsSchema(),
			"execution_profile":    ragExecutionProfileSchema(),
			"idempotency_key":      map[string]any{"type": "string"},
		}),
		ragToolDefinition("summarize_logs", "Retrieve, cluster, deduplicate, and compress large local logs into timestamp-preserving evidence.", []string{"input_refs"}, map[string]any{
			"input_refs":           inputRefsSchema(),
			"retrieval_strategies": retrievalStrategiesSchema(),
			"task_params":          map[string]any{"type": "object"},
			"constraints":          ragConstraintsSchema(),
			"execution_profile":    ragExecutionProfileSchema(),
			"idempotency_key":      map[string]any{"type": "string"},
		}),
		ragToolDefinition("inspect_repo", "Build or reuse local repository indexes and return compressed code evidence for a question.", []string{"input_refs", "task_params"}, map[string]any{
			"input_refs":           inputRefsSchema(),
			"retrieval_strategies": retrievalStrategiesSchema(),
			"task_params":          map[string]any{"type": "object"},
			"constraints":          ragConstraintsSchema(),
			"execution_profile":    ragExecutionProfileSchema(),
			"idempotency_key":      map[string]any{"type": "string"},
		}),
		ragToolDefinition("propose_patch", "Generate a local candidate patch package from evidence packs and authorized repository paths.", []string{"problem", "input_refs"}, map[string]any{
			"problem":              map[string]any{"type": "string"},
			"input_refs":           inputRefsSchema(),
			"retrieval_strategies": retrievalStrategiesSchema(),
			"task_params":          map[string]any{"type": "object"},
			"constraints":          ragConstraintsSchema(),
			"execution_profile":    ragExecutionProfileSchema(),
			"idempotency_key":      map[string]any{"type": "string"},
		}),
		{
			"name":        "submit_local_job",
			"description": "Submit a local analysis or inference task to the broker.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"task_type", "input_refs", "output_schema"},
				"properties": map[string]any{
					"task_type":         map[string]any{"type": "string"},
					"input_refs":        inputRefsSchema(),
					"task_params":       map[string]any{"type": "object"},
					"constraints":       ragConstraintsSchema(),
					"execution_profile": ragExecutionProfileSchema(),
					"orchestration":     orchestrationSchema(),
					"output_schema":     outputSchemaRefSchema(),
					"idempotency_key":   map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "submit_parallel_jobs",
			"description": "Submit many child jobs under one logical root investigation.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"task_type", "children", "output_schema"},
				"properties": map[string]any{
					"task_type":         map[string]any{"type": "string"},
					"task_params":       map[string]any{"type": "object"},
					"constraints":       map[string]any{"type": "object"},
					"execution_profile": map[string]any{"type": "object"},
					"output_schema":     map[string]any{"type": "object"},
					"root_job_id":       map[string]any{"type": "string"},
					"parent_job_id":     map[string]any{"type": "string"},
					"strategy":          map[string]any{"type": "string"},
					"children":          map[string]any{"type": "array"},
					"reducer":           map[string]any{"type": "object"},
				},
			},
		},
		{
			"name":        "get_job_status",
			"description": "Retrieve the current state of a previously submitted local job.",
			"inputSchema": simpleJobIDSchema(),
		},
		{
			"name":        "get_root_job_status",
			"description": "Retrieve aggregate status for a root investigation spanning many child jobs.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"root_job_id"},
				"properties": map[string]any{
					"root_job_id": map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "retry_failed_root_shards",
			"description": "Retry only the currently failed shards for a root investigation, optionally resubmitting its reducer.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"root_job_id"},
				"properties": map[string]any{
					"root_job_id":       map[string]any{"type": "string"},
					"include_cancelled": map[string]any{"type": "boolean"},
					"resubmit_reducer":  map[string]any{"type": "boolean"},
				},
			},
		},
		{
			"name":        "release_deferred_root_chunks",
			"description": "Force immediate release of deferred child chunks for a root investigation, optionally bounded to a one-shot number of batches.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"root_job_id"},
				"properties": map[string]any{
					"root_job_id":            map[string]any{"type": "string"},
					"max_additional_batches": map[string]any{"type": "integer"},
				},
			},
		},
		{
			"name":        "fetch_result",
			"description": "Fetch the structured result and artifacts for a completed local job.",
			"inputSchema": simpleJobIDSchema(),
		},
		{
			"name":        "get_retry_recommendation",
			"description": "Return the broker-generated retry recommendation for a completed local job, if one exists.",
			"inputSchema": simpleJobIDSchema(),
		},
		{
			"name":        "retry_with_recommended_profile",
			"description": "Submit a new job using the broker-recommended execution profile from a completed local job.",
			"inputSchema": simpleJobIDSchema(),
		},
		{
			"name":        "fetch_job_logs",
			"description": "Fetch redacted stdout and stderr from a local worker run.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"job_id"},
				"properties": map[string]any{
					"job_id":    map[string]any{"type": "string"},
					"stream":    map[string]any{"type": "string", "enum": []string{"stdout", "stderr", "combined"}},
					"max_bytes": map[string]any{"type": "integer"},
				},
			},
		},
		{
			"name":        "cancel_job",
			"description": "Cancel a queued or running local job.",
			"inputSchema": simpleJobIDSchema(),
		},
		{
			"name":        "list_local_capabilities",
			"description": "List the task types, schemas, execution modes, and backends currently supported by the broker.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func simpleJobIDSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"job_id"},
		"properties": map[string]any{
			"job_id": map[string]any{"type": "string"},
		},
	}
}

func capabilitiesPayload() map[string]any {
	return map[string]any{
		"task_types": []map[string]any{
			{
				"name":   "document_summary",
				"schema": "document_summary_v1",
				"inputs": []string{"file"},
			},
			{
				"name":   "log_analysis",
				"schema": "log_analysis_v1",
				"inputs": []string{"file"},
			},
			{
				"name":   "repo_summary",
				"schema": "repo_summary_v1",
				"inputs": []string{"directory", "repo"},
			},
			{
				"name":   "rag_compress",
				"schema": "rag_evidence_pack_v1",
				"inputs": []string{"file", "repo", "log", "document", "artifact"},
			},
			{
				"name":   "debug_with_local_context",
				"schema": "debug_evidence_pack_v1",
				"inputs": []string{"repo", "log", "artifact"},
			},
			{
				"name":   "summarize_logs",
				"schema": "log_evidence_pack_v1",
				"inputs": []string{"log"},
			},
			{
				"name":   "inspect_repo",
				"schema": "repo_inspection_pack_v1",
				"inputs": []string{"repo", "directory"},
			},
			{
				"name":   "propose_patch",
				"schema": "patch_proposal_pack_v1",
				"inputs": []string{"repo", "artifact"},
			},
		},
		"backends": []map[string]any{
			{
				"name":         "slurm",
				"modes":        []string{"stub", "command"},
				"default_mode": "stub",
			},
			{
				"name":         "local",
				"modes":        []string{"stub", "command"},
				"default_mode": "command",
			},
		},
		"tools": []string{
			"rag_compress",
			"debug_with_local_context",
			"summarize_logs",
			"inspect_repo",
			"propose_patch",
			"submit_local_job",
			"submit_parallel_jobs",
			"get_job_status",
			"get_root_job_status",
			"retry_failed_root_shards",
			"release_deferred_root_chunks",
			"fetch_result",
			"get_retry_recommendation",
			"retry_with_recommended_profile",
			"cancel_job",
			"list_local_capabilities",
		},
		"orchestration": map[string]any{
			"independent_parallel_jobs":  true,
			"parent_child_metadata":      true,
			"client_orchestrated_fanout": true,
			"aggregator_jobs":            true,
			"server_side_dag_scheduler":  false,
		},
		"cache": map[string]any{
			"exact_match_tasks": []string{"document_summary", "log_analysis", "repo_summary", "rag_compress", "summarize_logs", "inspect_repo", "debug_with_local_context"},
		},
	}
}

func decodeRAGToolSubmitRequest(raw json.RawMessage, taskType, schema string) (types.SubmitJobRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return types.SubmitJobRequest{}, err
	}
	var req types.SubmitJobRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return types.SubmitJobRequest{}, err
	}
	req.TaskType = taskType
	req.OutputSchema = types.OutputSchemaRef{Name: schema}
	req.TaskParams = normalizeRAGTaskParams(req.TaskParams, payload, taskType)
	return req, nil
}

func normalizeRAGTaskParams(taskParams map[string]any, payload map[string]any, taskType string) map[string]any {
	out := make(map[string]any, len(taskParams)+2)
	for k, v := range taskParams {
		out[k] = v
	}
	if value, ok := payload["query"]; ok {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out["query"] = text
		}
	}
	if value, ok := payload["problem"]; ok {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out["problem"] = text
		}
	}
	if value, ok := payload["retrieval_strategies"]; ok {
		if strategies, ok := value.([]any); ok && len(strategies) > 0 {
			out["retrieval_strategies"] = strategies
		}
	}
	if taskType == "debug_with_local_context" {
		if _, ok := out["problem"]; !ok {
			if text, ok := payload["problem"].(string); ok {
				out["problem"] = text
			}
		}
	}
	return out
}

func ragToolDefinition(name, description string, required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type":                 "object",
			"required":             required,
			"additionalProperties": false,
			"properties":           properties,
		},
	}
}

func inputRefsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"required":             []string{"type", "uri"},
			"additionalProperties": false,
			"properties": map[string]any{
				"type": map[string]any{
					"type": "string",
					"enum": []string{"file", "repo", "log", "document", "artifact", "directory"},
				},
				"uri":            map[string]any{"type": "string"},
				"content_hash":   map[string]any{"type": "string"},
				"classification": map[string]any{"type": "string"},
				"metadata":       map[string]any{"type": "object"},
			},
		},
	}
}

func orchestrationSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"parent_job_id":      map[string]any{"type": "string"},
			"root_job_id":        map[string]any{"type": "string"},
			"strategy":           map[string]any{"type": "string"},
			"shard_key":          map[string]any{"type": "string"},
			"shard_index":        map[string]any{"type": "integer"},
			"shard_count":        map[string]any{"type": "integer"},
			"aggregation_key":    map[string]any{"type": "string"},
			"depends_on_job_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

func outputSchemaRefSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             []string{"name"},
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
}

func ragConstraintsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"retrieved_chunk_budget":       map[string]any{"type": "integer"},
			"per_chunk_compression_budget": map[string]any{"type": "integer"},
			"final_evidence_pack_budget":   map[string]any{"type": "integer"},
			"remote_model_context_budget":  map[string]any{"type": "integer"},
			"max_runtime_seconds":          map[string]any{"type": "integer"},
			"confidentiality":              map[string]any{"type": "string"},
		},
	}
}

func retrievalStrategiesSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
			"enum": []string{"ripgrep", "bm25", "tree_sitter", "embeddings", "stack_trace_path", "git_diff_history", "artifact_context"},
		},
	}
}

func ragExecutionProfileSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"backend": map[string]any{"type": "string"},
			"tier": map[string]any{
				"type": "string",
				"enum": []string{"cpu-rag-indexing", "p40-rag-compression", "a100-reasoning"},
			},
			"model": map[string]any{"type": "string"},
			"runtime": map[string]any{
				"type": "string",
				"enum": []string{"llama.cpp", "vllm", "sglang", "deterministic"},
			},
			"qos":        map[string]any{"type": "string"},
			"nodelist":   map[string]any{"type": "string"},
			"constraint": map[string]any{"type": "string"},
		},
	}
}

func readMessage(r *bufio.Reader) ([]byte, messageFraming, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, framingContentLength, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if length == 0 && strings.HasPrefix(strings.TrimSpace(trimmed), "{") {
			return []byte(strings.TrimSpace(trimmed)), framingNDJSON, nil
		}
		line = trimmed
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			_, err := fmt.Sscanf(line, "Content-Length: %d", &length)
			if err != nil {
				_, err = fmt.Sscanf(line, "content-length: %d", &length)
				if err != nil {
					return nil, framingContentLength, err
				}
			}
		}
	}
	if length <= 0 {
		return nil, framingContentLength, fmt.Errorf("missing content length")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, framingContentLength, err
	}
	return payload, framingContentLength, nil
}

func writeMessage(w *bufio.Writer, payload any, framing messageFraming) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if framing == framingNDJSON {
		if _, err := w.Write(append(data, '\n')); err != nil {
			return err
		}
		return w.Flush()
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	return w.Flush()
}
