package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/authz"
	"github.com/limr/ollama-slurm/broker/pkg/policy"
	"github.com/limr/ollama-slurm/broker/pkg/service"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type Handler struct {
	service       *service.Service
	authenticator *auth.Authenticator
	auditLogPath  string
	mux           *http.ServeMux
}

func NewHandler(svc *service.Service, authenticator *auth.Authenticator) *Handler {
	return NewHandlerWithAudit(svc, authenticator, "")
}

func NewHandlerWithAudit(svc *service.Service, authenticator *auth.Authenticator, auditLogPath string) *Handler {
	h := &Handler{
		service:       svc,
		authenticator: authenticator,
		auditLogPath:  auditLogPath,
		mux:           http.NewServeMux(),
	}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		h.mux.ServeHTTP(w, r)
		return
	}
	principal, err := h.authenticator.Authenticate(r)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	h.mux.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
}

func (h *Handler) routes() {
	h.mux.HandleFunc("/healthz", h.handleHealth)
	h.mux.HandleFunc("/v1/system/audit-health", h.handleAuditHealth)
	h.mux.HandleFunc("/v1/jobs", h.handleJobs)
	h.mux.HandleFunc("/v1/jobs/", h.handleJobByID)
	h.mux.HandleFunc("/v1/roots/", h.handleRootByID)
	h.mux.HandleFunc("/v1/rag/compressions", h.handleRAGAlias("rag_compress", "rag_evidence_pack_v1"))
	h.mux.HandleFunc("/v1/rag/debug-sessions", h.handleRAGAlias("debug_with_local_context", "debug_evidence_pack_v1"))
	h.mux.HandleFunc("/v1/logs:summarize", h.handleRAGAlias("summarize_logs", "log_evidence_pack_v1"))
	h.mux.HandleFunc("/v1/repos:inspect", h.handleRAGAlias("inspect_repo", "repo_inspection_pack_v1"))
	h.mux.HandleFunc("/v1/patches:propose", h.handleRAGAlias("propose_patch", "patch_proposal_pack_v1"))
	h.mux.HandleFunc("/v1/rag/evidence-packs/", h.handleRAGEvidencePackMetadata)
	h.mux.HandleFunc("/v1/rag/indexes/", h.handleRAGIndexMetadata)
	h.mux.HandleFunc("/v1/rag/cache:lookup", h.handleRAGCacheLookup)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func (h *Handler) handleRAGAlias(taskType, schemaName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid job request body")
			return
		}
		var req types.SubmitJobRequest
		reqBytes, _ := json.Marshal(payload)
		if err := json.Unmarshal(reqBytes, &req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid job request body")
			return
		}
		req.TaskType = taskType
		req.OutputSchema = types.OutputSchemaRef{Name: schemaName}
		req.TaskParams = normalizeRAGTaskParams(req.TaskParams, payload, taskType)
		resp, err := h.service.SubmitJob(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, resp)
	}
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

func (h *Handler) handleAuditHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	principal := auth.PrincipalFromContext(r.Context())
	if !auth.IsAdmin(principal) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "audit health requires admin role")
		return
	}
	if h.auditLogPath == "" {
		writeError(w, http.StatusNotImplemented, "NOT_CONFIGURED", "audit log path is not configured")
		return
	}
	result, err := audit.VerifyFile(h.auditLogPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	status := http.StatusOK
	if !result.Valid {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON request body")
			return
		}
		if _, ok := payload["children"]; ok {
			reqBytes, _ := json.Marshal(payload)
			var req types.SubmitParallelJobsRequest
			if err := json.Unmarshal(reqBytes, &req); err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid parallel job request body")
				return
			}
			resp, err := h.service.SubmitParallelJobs(r.Context(), req)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, resp)
			return
		}
		reqBytes, _ := json.Marshal(payload)
		var req types.SubmitJobRequest
		if err := json.Unmarshal(reqBytes, &req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid job request body")
			return
		}

		resp, err := h.service.SubmitJob(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, resp)
	case http.MethodGet:
		h.handleListJobs(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	}
}

func (h *Handler) handleJobByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if strings.HasSuffix(path, ":cancel") {
		jobID := strings.TrimSuffix(path, ":cancel")
		h.handleCancelJob(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, ":retry-recommended") {
		jobID := strings.TrimSuffix(path, ":retry-recommended")
		h.handleRetryRecommendedJob(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/retry-recommendation") {
		jobID := strings.TrimSuffix(path, "/retry-recommendation")
		h.handleGetRetryRecommendation(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/result") {
		jobID := strings.TrimSuffix(path, "/result")
		h.handleFetchResult(w, r, jobID)
		return
	}
	if strings.HasSuffix(path, "/logs") {
		jobID := strings.TrimSuffix(path, "/logs")
		h.handleFetchLogs(w, r, jobID)
		return
	}
	h.handleGetJob(w, r, path)
}

func (h *Handler) handleRAGEvidencePackMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/rag/evidence-packs/")
	if !strings.HasSuffix(path, "/metadata") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	artifactID := strings.TrimSuffix(path, "/metadata")
	meta, err := h.service.GetArtifactMetadata(r.Context(), artifactID, map[string]struct{}{"evidence_pack": {}})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleRAGIndexMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/rag/indexes/")
	if !strings.HasSuffix(path, "/metadata") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}
	artifactID := strings.TrimSuffix(path, "/metadata")
	meta, err := h.service.GetArtifactMetadata(r.Context(), artifactID, map[string]struct{}{"retrieval_result": {}, "chunk_manifest": {}})
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *Handler) handleRAGCacheLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var req types.SubmitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid cache lookup request body")
		return
	}
	resp, err := h.service.LookupCache(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleGetRetryRecommendation(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	rec, err := h.service.GetJobRetryRecommendation(r.Context(), jobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (h *Handler) handleRetryRecommendedJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	resp, err := h.service.RetryJobWithRecommendation(r.Context(), jobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (h *Handler) handleRootByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/roots/")
	if strings.HasSuffix(path, ":retry-failed") {
		rootJobID := strings.TrimSuffix(path, ":retry-failed")
		h.handleRetryFailedRootShards(w, r, rootJobID)
		return
	}
	if strings.HasSuffix(path, ":release-deferred") {
		rootJobID := strings.TrimSuffix(path, ":release-deferred")
		h.handleReleaseDeferredRootChunks(w, r, rootJobID)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	rootJobID := path
	status, err := h.service.GetRootJobStatus(r.Context(), rootJobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) handleRetryFailedRootShards(w http.ResponseWriter, r *http.Request, rootJobID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	req := types.RetryFailedRootShardsRequest{
		RootJobID:       rootJobID,
		ResubmitReducer: true,
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid retry request body")
			return
		}
		if strings.TrimSpace(req.RootJobID) == "" {
			req.RootJobID = rootJobID
		}
	}
	resp, err := h.service.RetryFailedRootShards(r.Context(), req)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (h *Handler) handleReleaseDeferredRootChunks(w http.ResponseWriter, r *http.Request, rootJobID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	req := types.ReleaseDeferredRootChunksRequest{
		RootJobID: rootJobID,
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid release request body")
			return
		}
		if strings.TrimSpace(req.RootJobID) == "" {
			req.RootJobID = rootJobID
		}
	}
	resp, err := h.service.ReleaseDeferredRootChunks(r.Context(), req)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (h *Handler) handleGetJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	job, err := h.service.GetJob(r.Context(), jobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.service.ListJobs(r.Context())
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

func (h *Handler) handleFetchResult(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	release, err := h.service.GetReleasedResult(r.Context(), jobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, release)
}

func (h *Handler) handleFetchLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	stream := r.URL.Query().Get("stream")
	maxBytes := 0
	if raw := r.URL.Query().Get("max_bytes"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &maxBytes); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid max_bytes")
			return
		}
	}

	logs, err := h.service.GetJobLogs(r.Context(), jobID, stream, maxBytes)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported log stream") {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (h *Handler) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}

	resp, err := h.service.CancelJob(r.Context(), jobID)
	if err != nil {
		handleServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, authz.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, policy.ErrPolicyDenied):
		writeError(w, http.StatusForbidden, "POLICY_DENIED", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
