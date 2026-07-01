package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/limr/ollama-slurm/broker/pkg/audit"
	"github.com/limr/ollama-slurm/broker/pkg/auth"
	"github.com/limr/ollama-slurm/broker/pkg/authz"
	"github.com/limr/ollama-slurm/broker/pkg/backends"
	"github.com/limr/ollama-slurm/broker/pkg/cache"
	"github.com/limr/ollama-slurm/broker/pkg/config"
	"github.com/limr/ollama-slurm/broker/pkg/policy"
	"github.com/limr/ollama-slurm/broker/pkg/schemas"
	"github.com/limr/ollama-slurm/broker/pkg/store"
	"github.com/limr/ollama-slurm/broker/pkg/types"
)

type Service struct {
	store       store.JobStore
	backend     backends.Backend
	logger      *log.Logger
	auditLogger audit.Logger
	runRoot     string
	repoRoot    string
	models      modelProfiles
	runtimes    runtimeProfiles
	options     Options
}

type modelProfiles struct {
	cpu  string
	p40  string
	a100 string
}

type runtimeProfiles struct {
	llamaCPP runtimeConnection
	vllm     runtimeConnection
	sglang   runtimeConnection
}

type runtimeConnection struct {
	BaseURL        string
	TimeoutSeconds int
}

type Options struct {
	ParallelMaxBatchSize           int
	ParallelMaxActiveBatches       int
	RootActionMaxAdditionalBatches int
	RootActionMaxRetriedShards     int
}

func New(jobStore store.JobStore, backend backends.Backend, logger *log.Logger, runRoot, repoRoot string) *Service {
	return NewWithAuditAndOptionsAndConfig(jobStore, backend, logger, audit.NewNopLogger(), runRoot, repoRoot, Options{}, nil)
}

func NewWithAudit(jobStore store.JobStore, backend backends.Backend, logger *log.Logger, auditLogger audit.Logger, runRoot, repoRoot string) *Service {
	return NewWithAuditAndOptionsAndConfig(jobStore, backend, logger, auditLogger, runRoot, repoRoot, Options{}, nil)
}

func NewWithAuditAndOptions(jobStore store.JobStore, backend backends.Backend, logger *log.Logger, auditLogger audit.Logger, runRoot, repoRoot string, opts Options) *Service {
	return NewWithAuditAndOptionsAndConfig(jobStore, backend, logger, auditLogger, runRoot, repoRoot, opts, nil)
}

func NewWithAuditAndOptionsAndConfig(jobStore store.JobStore, backend backends.Backend, logger *log.Logger, auditLogger audit.Logger, runRoot, repoRoot string, opts Options, cfg *config.Config) *Service {
	if auditLogger == nil {
		auditLogger = audit.NewNopLogger()
	}
	models := defaultModelProfiles()
	runtimes := defaultRuntimeProfiles()
	if cfg != nil {
		models = modelProfiles{
			cpu:  strings.TrimSpace(cfg.ModelProfileCPU),
			p40:  strings.TrimSpace(cfg.ModelProfileP40),
			a100: strings.TrimSpace(cfg.ModelProfileA100),
		}
		runtimes = runtimeProfiles{
			llamaCPP: runtimeConnection{
				BaseURL:        strings.TrimSpace(cfg.RuntimeLlamaCPPBaseURL),
				TimeoutSeconds: cfg.RuntimeLlamaCPPTimeoutSeconds,
			},
			vllm: runtimeConnection{
				BaseURL:        strings.TrimSpace(cfg.RuntimeVLLMBaseURL),
				TimeoutSeconds: cfg.RuntimeVLLMTimeoutSeconds,
			},
			sglang: runtimeConnection{
				BaseURL:        strings.TrimSpace(cfg.RuntimeSGLangBaseURL),
				TimeoutSeconds: cfg.RuntimeSGLangTimeoutSeconds,
			},
		}
	}
	return &Service{
		store:       jobStore,
		backend:     backend,
		logger:      logger,
		auditLogger: auditLogger,
		runRoot:     runRoot,
		repoRoot:    repoRoot,
		models:      models,
		runtimes:    runtimes,
		options:     normalizeOptions(opts),
	}
}

func (s *Service) SubmitJob(ctx context.Context, req types.SubmitJobRequest) (types.SubmitJobResponse, error) {
	if req.TaskType == "" {
		return types.SubmitJobResponse{}, errors.New("task_type is required")
	}
	if req.OutputSchema.Name == "" {
		return types.SubmitJobResponse{}, errors.New("output_schema.name is required")
	}
	req.ExecutionProfile = s.applyExecutionProfileDefaults(req.ExecutionProfile)

	taskParams := cloneTaskParams(req.TaskParams)
	taskParams["_broker_run_root"] = s.runRoot
	taskParams["_broker_repo_root"] = s.repoRoot
	req.TaskParams = taskParams

	cacheKey, cacheable, err := cache.KeyForRequest(req)
	if err != nil {
		return types.SubmitJobResponse{}, fmt.Errorf("compute cache key: %w", err)
	}
	if cacheable {
		cachedJob, err := cache.FindCompletedJobByCacheKey(ctx, s.store, cacheKey)
		if err != nil {
			return types.SubmitJobResponse{}, fmt.Errorf("lookup cache: %w", err)
		}
		if cachedJob != nil {
			now := time.Now().UTC()
			principal := auth.PrincipalFromContext(ctx)
			job := types.Job{
				ID:                     newJobID(),
				TaskType:               req.TaskType,
				State:                  types.JobStateSucceeded,
				SubmittedBy:            principal.Actor,
				Request:                req,
				Result:                 cachedJob.Result,
				RuntimeDiagnostics:     cloneMap(cachedJob.RuntimeDiagnostics),
				ExecutionQuality:       cachedJob.ExecutionQuality,
				DegradedLocalExecution: cachedJob.DegradedLocalExecution,
				RetryRecommended:       cachedJob.RetryRecommended,
				Artifacts:              cloneArtifacts(cachedJob.Artifacts),
				CreatedAt:              now,
				UpdatedAt:              now,
				SubmittedAt:            now,
				StartedAt:              &now,
				CompletedAt:            &now,
				CacheKey:               cacheKey,
				CacheStatus:            "hit",
				BackendKind:            "cache",
				BackendState:           "CACHE_HIT",
			}
			if err := s.store.CreateJob(ctx, job); err != nil {
				return types.SubmitJobResponse{}, fmt.Errorf("store cached job: %w", err)
			}
			s.logger.Printf("cache hit job=%s source_job=%s task_type=%s", job.ID, cachedJob.ID, job.TaskType)
			s.audit(ctx, "job.submit", "success", &job, map[string]any{
				"cache_status": "hit",
				"backend_kind": job.BackendKind,
			})
			return types.SubmitJobResponse{
				JobID:     job.ID,
				State:     job.State,
				Cache:     types.CacheStatus{Status: job.CacheStatus},
				StatusURL: "/v1/jobs/" + job.ID,
			}, nil
		}
	}

	now := time.Now().UTC()
	principal := auth.PrincipalFromContext(ctx)
	jobID := newJobID()
	orchestration := normalizeOrchestration(jobID, req.Orchestration)
	job := types.Job{
		ID:            jobID,
		TaskType:      req.TaskType,
		State:         types.JobStateAccepted,
		SubmittedBy:   principal.Actor,
		Request:       req,
		CreatedAt:     now,
		UpdatedAt:     now,
		SubmittedAt:   now,
		CacheKey:      cacheKey,
		CacheStatus:   "miss",
		ParentJobID:   orchestration.ParentJobID,
		RootJobID:     orchestration.RootJobID,
		Orchestration: orchestration,
	}

	if err := s.stageExecutionBundle(ctx, job); err != nil {
		return types.SubmitJobResponse{}, fmt.Errorf("stage execution bundle: %w", err)
	}

	submitResp, err := s.backend.SubmitRun(ctx, job)
	if err != nil {
		return types.SubmitJobResponse{}, fmt.Errorf("submit backend run: %w", err)
	}
	job.State = submitResp.InitialState
	job.BackendKind = submitResp.BackendKind
	job.BackendRunID = submitResp.BackendRunID

	if err := s.store.CreateJob(ctx, job); err != nil {
		return types.SubmitJobResponse{}, fmt.Errorf("store job: %w", err)
	}

	s.logger.Printf("submitted job=%s task_type=%s backend_run_id=%s", job.ID, job.TaskType, job.BackendRunID)
	s.audit(ctx, "job.submit", "success", &job, map[string]any{
		"cache_status": job.CacheStatus,
		"backend_kind": job.BackendKind,
	})

	return types.SubmitJobResponse{
		JobID:     job.ID,
		State:     job.State,
		Cache:     types.CacheStatus{Status: job.CacheStatus},
		StatusURL: "/v1/jobs/" + job.ID,
	}, nil
}

func (s *Service) SubmitParallelJobs(ctx context.Context, req types.SubmitParallelJobsRequest) (types.SubmitParallelJobsResponse, error) {
	if req.TaskType == "" {
		return types.SubmitParallelJobsResponse{}, errors.New("task_type is required")
	}
	if req.OutputSchema.Name == "" {
		return types.SubmitParallelJobsResponse{}, errors.New("output_schema.name is required")
	}
	if len(req.Children) == 0 {
		return types.SubmitParallelJobsResponse{}, errors.New("children is required")
	}

	rootJobID := strings.TrimSpace(req.RootJobID)
	if rootJobID == "" {
		rootJobID = newRootJobID()
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "fanout_child"
	}

	children := make([]types.ParallelChildSubmission, len(req.Children))
	childJobIDs := make([]string, len(req.Children))
	childBackendRunIDs := make([]string, 0, len(req.Children))
	type pendingChild struct {
		index  int
		child  types.ParallelChildRequest
		req    types.SubmitJobRequest
		job    types.Job
		status types.SubmitJobResponse
		chunk  int
	}
	pending := make([]pendingChild, 0, len(req.Children))

	for index, child := range req.Children {
		taskParams := cloneTaskParams(req.TaskParams)
		for k, v := range child.TaskParams {
			taskParams[k] = v
		}
		submitReq := types.SubmitJobRequest{
			TaskType:         req.TaskType,
			InputRefs:        child.InputRefs,
			TaskParams:       taskParams,
			Constraints:      req.Constraints,
			ExecutionProfile: s.applyExecutionProfileDefaults(req.ExecutionProfile),
			OutputSchema:     req.OutputSchema,
			Orchestration: types.OrchestrationRequest{
				ParentJobID:     req.ParentJobID,
				RootJobID:       rootJobID,
				Strategy:        strategy,
				ShardKey:        child.ShardKey,
				ShardIndex:      child.ShardIndex,
				ShardCount:      child.ShardCount,
				AggregationKey:  firstNonEmpty(child.AggregationKey, ""),
				DependsOnJobIDs: append([]string(nil), child.DependsOnJobIDs...),
			},
		}

		enrichedParams := cloneTaskParams(submitReq.TaskParams)
		enrichedParams["_broker_run_root"] = s.runRoot
		enrichedParams["_broker_repo_root"] = s.repoRoot
		submitReq.TaskParams = enrichedParams

		cacheKey, cacheable, err := cache.KeyForRequest(submitReq)
		if err != nil {
			return types.SubmitParallelJobsResponse{}, fmt.Errorf("compute cache key for child shard %d: %w", child.ShardIndex, err)
		}
		if cacheable {
			cachedJob, err := cache.FindCompletedJobByCacheKey(ctx, s.store, cacheKey)
			if err != nil {
				return types.SubmitParallelJobsResponse{}, fmt.Errorf("lookup cache for child shard %d: %w", child.ShardIndex, err)
			}
			if cachedJob != nil {
				resp, err := s.SubmitJob(ctx, submitReq)
				if err != nil {
					return types.SubmitParallelJobsResponse{}, fmt.Errorf("submit cached child shard %d: %w", child.ShardIndex, err)
				}
				childJobIDs[index] = resp.JobID
				children[index] = types.ParallelChildSubmission{
					JobID:          resp.JobID,
					State:          resp.State,
					Cache:          resp.Cache,
					StatusURL:      resp.StatusURL,
					ShardKey:       child.ShardKey,
					ShardIndex:     child.ShardIndex,
					ShardCount:     child.ShardCount,
					AggregationKey: child.AggregationKey,
				}
				continue
			}
		}

		now := time.Now().UTC()
		principal := auth.PrincipalFromContext(ctx)
		jobID := newJobID()
		orchestration := normalizeOrchestration(jobID, submitReq.Orchestration)
		job := types.Job{
			ID:            jobID,
			TaskType:      submitReq.TaskType,
			State:         types.JobStateDispatching,
			SubmittedBy:   principal.Actor,
			Request:       submitReq,
			CreatedAt:     now,
			UpdatedAt:     now,
			SubmittedAt:   now,
			CacheKey:      cacheKey,
			CacheStatus:   "miss",
			ParentJobID:   orchestration.ParentJobID,
			RootJobID:     orchestration.RootJobID,
			Orchestration: orchestration,
		}
		if err := s.stageExecutionBundle(ctx, job); err != nil {
			return types.SubmitParallelJobsResponse{}, fmt.Errorf("stage child shard %d execution bundle: %w", child.ShardIndex, err)
		}
		pending = append(pending, pendingChild{
			index: index,
			child: child,
			req:   submitReq,
			job:   job,
			chunk: len(pending) / s.options.ParallelMaxBatchSize,
		})
	}

	if len(pending) > 0 {
		for i := range pending {
			pending[i].job.Request.TaskParams["_broker_dispatch_chunk"] = pending[i].chunk
			if err := s.store.CreateJob(ctx, pending[i].job); err != nil {
				return types.SubmitParallelJobsResponse{}, fmt.Errorf("store child shard %d: %w", pending[i].child.ShardIndex, err)
			}
			s.logger.Printf("created child job=%s task_type=%s root_job_id=%s state=%s", pending[i].job.ID, pending[i].job.TaskType, pending[i].job.RootJobID, pending[i].job.State)
			s.audit(ctx, "job.submit", "success", &pending[i].job, map[string]any{
				"cache_status": pending[i].job.CacheStatus,
				"backend_kind": pending[i].job.BackendKind,
			})
			children[pending[i].index] = types.ParallelChildSubmission{
				JobID:          pending[i].job.ID,
				State:          pending[i].job.State,
				Cache:          types.CacheStatus{Status: pending[i].job.CacheStatus},
				StatusURL:      "/v1/jobs/" + pending[i].job.ID,
				ShardKey:       pending[i].child.ShardKey,
				ShardIndex:     pending[i].child.ShardIndex,
				ShardCount:     pending[i].child.ShardCount,
				AggregationKey: pending[i].child.AggregationKey,
			}
			childJobIDs[pending[i].index] = pending[i].job.ID
		}

		if _, err := s.releaseDispatchingRootChildren(ctx, rootJobID, 0, false); err != nil {
			return types.SubmitParallelJobsResponse{}, fmt.Errorf("release initial child batches: %w", err)
		}
		for i, childResp := range children {
			job, err := s.store.GetJob(ctx, childResp.JobID)
			if err == nil {
				children[i].State = job.State
				childBackendRunIDs = appendIfNonEmpty(childBackendRunIDs, job.BackendRunID)
			}
		}
	}

	var reducerResp *types.SubmitJobResponse
	if req.Reducer != nil {
		orderedChildJobIDs := compactNonEmptyStrings(childJobIDs)
		reducerTaskType := firstNonEmpty(req.Reducer.TaskType, req.TaskType)
		reducerSchema := req.Reducer.OutputSchema
		if reducerSchema.Name == "" {
			reducerSchema = req.OutputSchema
		}
		reducerProfile := req.ExecutionProfile
		if req.Reducer.ExecutionProfile != (types.ExecutionProfile{}) {
			reducerProfile = req.Reducer.ExecutionProfile
		}
		reducerConstraints := req.Constraints
		if req.Reducer.Constraints != (types.Constraints{}) {
			reducerConstraints = req.Reducer.Constraints
		}
		reducerTaskParams := cloneTaskParams(req.TaskParams)
		for k, v := range req.Reducer.TaskParams {
			reducerTaskParams[k] = v
		}
		reducerTaskParams["child_job_ids"] = append([]string(nil), orderedChildJobIDs...)
		reducerTaskParams["root_job_id"] = rootJobID
		reducerTaskParams["_dependency_backend_run_ids"] = append([]string(nil), childBackendRunIDs...)
		reducerSubmitReq := types.SubmitJobRequest{
			TaskType:         reducerTaskType,
			InputRefs:        append([]types.InputRef(nil), req.Reducer.InputRefs...),
			TaskParams:       reducerTaskParams,
			Constraints:      reducerConstraints,
			ExecutionProfile: s.applyExecutionProfileDefaults(reducerProfile),
			OutputSchema:     reducerSchema,
			Orchestration: types.OrchestrationRequest{
				ParentJobID:     req.ParentJobID,
				RootJobID:       rootJobID,
				Strategy:        "aggregator",
				AggregationKey:  firstNonEmpty(req.Reducer.AggregationKey, "aggregate"),
				DependsOnJobIDs: append([]string(nil), orderedChildJobIDs...),
			},
		}
		if hasDispatchingChildrenForJobIDs(ctx, s.store, orderedChildJobIDs) {
			resp, err := s.createDeferredReducer(ctx, reducerSubmitReq)
			if err != nil {
				return types.SubmitParallelJobsResponse{}, fmt.Errorf("create deferred reducer: %w", err)
			}
			reducerResp = resp
		} else {
			resp, err := s.SubmitJob(ctx, reducerSubmitReq)
			if err != nil {
				return types.SubmitParallelJobsResponse{}, fmt.Errorf("submit reducer: %w", err)
			}
			reducerResp = &resp
		}
	}

	return types.SubmitParallelJobsResponse{
		RootJobID:   rootJobID,
		ParentJobID: req.ParentJobID,
		Strategy:    strategy,
		ChildCount:  len(children),
		Children:    children,
		ReducerJob:  reducerResp,
	}, nil
}

func normalizeOrchestration(jobID string, req types.OrchestrationRequest) *types.OrchestrationInfo {
	rootJobID := strings.TrimSpace(req.RootJobID)
	parentJobID := strings.TrimSpace(req.ParentJobID)
	if rootJobID == "" {
		if parentJobID != "" {
			rootJobID = parentJobID
		} else {
			rootJobID = jobID
		}
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		if parentJobID != "" {
			strategy = "fanout_child"
		} else {
			strategy = "standalone"
		}
	}
	return &types.OrchestrationInfo{
		ParentJobID:     parentJobID,
		RootJobID:       rootJobID,
		Strategy:        strategy,
		ShardKey:        strings.TrimSpace(req.ShardKey),
		ShardIndex:      req.ShardIndex,
		ShardCount:      req.ShardCount,
		AggregationKey:  strings.TrimSpace(req.AggregationKey),
		DependsOnJobIDs: append([]string(nil), req.DependsOnJobIDs...),
	}
}

func (s *Service) GetJob(ctx context.Context, jobID string) (types.Job, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		s.auditDeniedLookup(ctx, "job.get_status", jobID, err)
		return types.Job{}, err
	}
	s.audit(ctx, "job.get_status", "success", &job, nil)
	return job, nil
}

func (s *Service) getJob(ctx context.Context, jobID string) (types.Job, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return types.Job{}, err
	}
	if err := authz.AuthorizeJobAccess(auth.PrincipalFromContext(ctx), job); err != nil {
		return types.Job{}, err
	}
	if job.RootJobID != "" {
		_, _ = s.releaseDispatchingRootChildren(ctx, job.RootJobID, 0, false)
		if refreshed, refreshErr := s.store.GetJob(ctx, jobID); refreshErr == nil {
			job = refreshed
		}
	}

	if job.BackendRunID != "" && !isTerminal(job.State) {
		runStatus, err := s.backend.GetRun(ctx, job.BackendRunID)
		if err == nil {
			if runStatus.State == "" || runStatus.State == job.State {
				if job.BackendState == "" && runStatus.RawState != "" {
					job.BackendState = runStatus.RawState
					job.BackendExitCode = runStatus.ExitCode
					_ = s.store.UpdateJob(ctx, job)
				}
			} else {
				now := time.Now().UTC()
				job.State = runStatus.State
				job.BackendState = runStatus.RawState
				job.BackendExitCode = runStatus.ExitCode
				job.UpdatedAt = now
				if runStatus.State == types.JobStateRunning && job.StartedAt == nil {
					job.StartedAt = &now
				}
				if isTerminal(runStatus.State) {
					job.CompletedAt = &now
				}
				if err := s.store.UpdateJob(ctx, job); err != nil {
					return types.Job{}, fmt.Errorf("update refreshed job: %w", err)
				}
			}
		}
	}

	if updated, err := s.refreshProgress(ctx, job); err == nil {
		job = updated
	}

	if job.State == types.JobStateSucceeded && job.Result == nil {
		updated, err := s.ingestRunOutputs(ctx, job)
		if err == nil {
			job = updated
		} else {
			now := time.Now().UTC()
			job.State = types.JobStateFailed
			job.ResultError = err.Error()
			job.UpdatedAt = now
			job.CompletedAt = &now
			_ = s.store.UpdateJob(ctx, job)
		}
	}

	return job, nil
}

func (s *Service) ListJobs(ctx context.Context) ([]types.Job, error) {
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	principal := auth.PrincipalFromContext(ctx)
	if auth.IsAdmin(principal) {
		s.audit(ctx, "job.list", "success", nil, map[string]any{
			"visible_count": len(jobs),
		})
		return jobs, nil
	}
	filtered := make([]types.Job, 0, len(jobs))
	for _, job := range jobs {
		if err := authz.AuthorizeJobAccess(principal, job); err == nil {
			filtered = append(filtered, job)
		}
	}
	s.audit(ctx, "job.list", "success", nil, map[string]any{
		"visible_count": len(filtered),
	})
	return filtered, nil
}

func (s *Service) loadRootJobsAuthorized(ctx context.Context, rootJobID string) ([]types.Job, error) {
	rootJobID = strings.TrimSpace(rootJobID)
	if rootJobID == "" {
		return nil, store.ErrNotFound
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]types.Job, 0, len(jobs))
	principal := auth.PrincipalFromContext(ctx)
	var unauthorized bool
	for _, job := range jobs {
		if job.RootJobID != rootJobID {
			continue
		}
		filtered = append(filtered, job)
		if err := authz.AuthorizeJobAccess(principal, job); err != nil {
			unauthorized = true
		}
	}
	if len(filtered) == 0 {
		return nil, store.ErrNotFound
	}
	if unauthorized {
		return nil, fmt.Errorf("%w: root %q includes inaccessible jobs", authz.ErrForbidden, rootJobID)
	}
	return filtered, nil
}

func (s *Service) authorizeForcedRootRelease(ctx context.Context, requestedBatches int) error {
	return s.authorizeForcedRootReleaseWithUsage(ctx, requestedBatches, 0)
}

func (s *Service) authorizeForcedRootReleaseWithUsage(ctx context.Context, requestedBatches, existingBatches int) error {
	if requestedBatches <= 0 {
		return nil
	}
	principal := auth.PrincipalFromContext(ctx)
	if auth.IsAdmin(principal) {
		return nil
	}
	if requestedBatches > s.options.RootActionMaxAdditionalBatches {
		return fmt.Errorf(
			"%w: requested max_additional_batches=%d exceeds non-admin limit %d",
			authz.ErrForbidden,
			requestedBatches,
			s.options.RootActionMaxAdditionalBatches,
		)
	}
	if existingBatches+requestedBatches > s.options.RootActionMaxAdditionalBatches {
		return fmt.Errorf(
			"%w: cumulative forced_release_batches=%d would exceed non-admin limit %d",
			authz.ErrForbidden,
			existingBatches+requestedBatches,
			s.options.RootActionMaxAdditionalBatches,
		)
	}
	return nil
}

func (s *Service) authorizeFailedShardRetry(ctx context.Context, requestedShards int) error {
	return s.authorizeFailedShardRetryWithUsage(ctx, requestedShards, 0)
}

func (s *Service) authorizeFailedShardRetryWithUsage(ctx context.Context, requestedShards, existingShards int) error {
	if requestedShards <= 0 {
		return nil
	}
	principal := auth.PrincipalFromContext(ctx)
	if auth.IsAdmin(principal) {
		return nil
	}
	if requestedShards > s.options.RootActionMaxRetriedShards {
		return fmt.Errorf(
			"%w: requested retried_shards=%d exceeds non-admin limit %d",
			authz.ErrForbidden,
			requestedShards,
			s.options.RootActionMaxRetriedShards,
		)
	}
	if existingShards+requestedShards > s.options.RootActionMaxRetriedShards {
		return fmt.Errorf(
			"%w: cumulative retried_shards=%d would exceed non-admin limit %d",
			authz.ErrForbidden,
			existingShards+requestedShards,
			s.options.RootActionMaxRetriedShards,
		)
	}
	return nil
}

func (s *Service) GetRootJobStatus(ctx context.Context, rootJobID string) (types.RootJobStatus, error) {
	filtered, err := s.loadRootJobsAuthorized(ctx, rootJobID)
	if err != nil {
		return types.RootJobStatus{}, err
	}
	_, _ = s.releaseDispatchingRootChildren(ctx, rootJobID, 0, false)
	filtered, err = s.loadRootJobsAuthorized(ctx, rootJobID)
	if err != nil {
		return types.RootJobStatus{}, err
	}

	status := types.RootJobStatus{
		RootJobID: rootJobID,
		State:     types.JobStateQueued,
	}
	status.DispatchingChildren, status.PendingChildren, status.ActiveChunks, status.PendingChunks = dispatchObservability(filtered)
	usage := rootActionUsage(filtered)
	status.ForcedReleasedChunks = usage.ForcedReleasedChunks
	status.RetriedShardActions = usage.RetriedShardActions
	effectiveChildren := effectiveShardJobs(filtered, false)
	effectiveReducers := effectiveReducerJobs(filtered)
	var reducer *types.Job
	aggregationKeys := map[string]struct{}{}
	for i := range effectiveChildren {
		job := effectiveChildren[i]
		status.TotalJobs++
		switch job.State {
		case types.JobStateAccepted, types.JobStateQueued, types.JobStateDispatching:
			status.QueuedJobs++
		case types.JobStateRunning:
			status.RunningJobs++
		case types.JobStateSucceeded:
			status.SucceededJobs++
		case types.JobStateFailed, types.JobStatePreempted, types.JobStateTimedOut:
			status.FailedJobs++
		case types.JobStateCancelled:
			status.CancelledJobs++
		}

		status.ChildJobIDs = append(status.ChildJobIDs, job.ID)
		if job.Orchestration != nil && job.Orchestration.AggregationKey != "" {
			aggregationKeys[job.Orchestration.AggregationKey] = struct{}{}
		}
		if job.ResultError != "" && status.RepresentativeError == "" {
			status.RepresentativeError = job.ResultError
		}
		if progressNewer(job.Progress, status.Progress) {
			status.Progress = job.Progress
		}
	}
	if len(effectiveReducers) > 0 {
		reducer = &effectiveReducers[0]
		status.TotalJobs++
		switch reducer.State {
		case types.JobStateAccepted, types.JobStateQueued, types.JobStateDispatching:
			status.QueuedJobs++
		case types.JobStateRunning:
			status.RunningJobs++
		case types.JobStateSucceeded:
			status.SucceededJobs++
		case types.JobStateFailed, types.JobStatePreempted, types.JobStateTimedOut:
			status.FailedJobs++
		case types.JobStateCancelled:
			status.CancelledJobs++
		}
		if reducer.Orchestration != nil && reducer.Orchestration.AggregationKey != "" {
			aggregationKeys[reducer.Orchestration.AggregationKey] = struct{}{}
		}
		if reducer.ResultError != "" && status.RepresentativeError == "" {
			status.RepresentativeError = reducer.ResultError
		}
		if progressNewer(reducer.Progress, status.Progress) {
			status.Progress = reducer.Progress
		}
	}

	if reducer != nil {
		status.ReducerJobID = reducer.ID
		status.ReducerState = reducer.State
		status.ReducerDeferred = reducer.State == types.JobStateDispatching && reducer.BackendRunID == ""
		applyReducerMetrics(&status, reducer.Result)
	}
	status.AggregationKeys = setKeys(aggregationKeys)
	status.State = aggregateRootState(status)
	return status, nil
}

func (s *Service) RetryFailedRootShards(ctx context.Context, req types.RetryFailedRootShardsRequest) (types.RetryFailedRootShardsResponse, error) {
	rootJobID := strings.TrimSpace(req.RootJobID)
	if rootJobID == "" {
		return types.RetryFailedRootShardsResponse{}, errors.New("root_job_id is required")
	}

	filtered, err := s.loadRootJobsAuthorized(ctx, rootJobID)
	if err != nil {
		return types.RetryFailedRootShardsResponse{}, err
	}

	effectiveChildren := effectiveShardJobs(filtered, req.IncludeCancelled)
	retryableCount := 0
	for _, job := range effectiveChildren {
		if shouldRetryShard(job, req.IncludeCancelled) {
			retryableCount++
		}
	}
	usage := rootActionUsage(filtered)
	if err := s.authorizeFailedShardRetryWithUsage(ctx, retryableCount, usage.RetriedShardActions); err != nil {
		return types.RetryFailedRootShardsResponse{}, err
	}
	response := types.RetryFailedRootShardsResponse{
		RootJobID: rootJobID,
	}
	currentEffective := make([]types.Job, 0, len(effectiveChildren))
	for _, job := range effectiveChildren {
		if shouldRetryShard(job, req.IncludeCancelled) {
			retryReq := retrySubmitRequest(job)
			retryReq.TaskParams["_broker_retry_action"] = true
			resp, err := s.SubmitJob(ctx, retryReq)
			if err != nil {
				return types.RetryFailedRootShardsResponse{}, fmt.Errorf("retry shard %s: %w", job.ID, err)
			}
			retriedJob, err := s.getJob(ctx, resp.JobID)
			if err != nil {
				return types.RetryFailedRootShardsResponse{}, fmt.Errorf("lookup retried shard %s: %w", resp.JobID, err)
			}
			currentEffective = append(currentEffective, retriedJob)
			response.RetriedShards = append(response.RetriedShards, types.RetriedShardSubmission{
				PreviousJobID:  job.ID,
				JobID:          resp.JobID,
				State:          resp.State,
				Cache:          resp.Cache,
				StatusURL:      resp.StatusURL,
				ShardKey:       shardKeyOf(job),
				ShardIndex:     shardIndexOf(job),
				ShardCount:     shardCountOf(job),
				AggregationKey: aggregationKeyOf(job),
			})
			continue
		}
		currentEffective = append(currentEffective, job)
		response.SkippedShards = append(response.SkippedShards, types.SkippedShardRetry{
			JobID:      job.ID,
			ShardKey:   shardKeyOf(job),
			ShardIndex: shardIndexOf(job),
			ShardCount: shardCountOf(job),
			Reason:     retrySkipReason(job, req.IncludeCancelled),
		})
	}
	response.RetriedCount = len(response.RetriedShards)
	response.SkippedCount = len(response.SkippedShards)
	response.CumulativeRetriedShards = usage.RetriedShardActions + response.RetriedCount
	response.RemainingRetriedShardBudget = remainingNonAdminBudget(s.options.RootActionMaxRetriedShards, response.CumulativeRetriedShards, auth.PrincipalFromContext(ctx))

	if req.ResubmitReducer && len(response.RetriedShards) > 0 {
		reducers := effectiveReducerJobs(filtered)
		if len(reducers) > 0 {
			resp, err := s.submitRetriedReducer(ctx, reducers[0], currentEffective)
			if err != nil {
				return types.RetryFailedRootShardsResponse{}, fmt.Errorf("resubmit reducer: %w", err)
			}
			response.ReducerJob = resp
		}
	}

	return response, nil
}

func (s *Service) ReleaseDeferredRootChunks(ctx context.Context, req types.ReleaseDeferredRootChunksRequest) (types.ReleaseDeferredRootChunksResponse, error) {
	rootJobID := strings.TrimSpace(req.RootJobID)
	if rootJobID == "" {
		return types.ReleaseDeferredRootChunksResponse{}, errors.New("root_job_id is required")
	}
	filtered, err := s.loadRootJobsAuthorized(ctx, rootJobID)
	if err != nil {
		return types.ReleaseDeferredRootChunksResponse{}, err
	}
	usage := rootActionUsage(filtered)
	if err := s.authorizeForcedRootReleaseWithUsage(ctx, req.MaxAdditionalBatches, usage.ForcedReleasedChunks); err != nil {
		return types.ReleaseDeferredRootChunksResponse{}, err
	}
	release, err := s.releaseDispatchingRootChildren(ctx, rootJobID, req.MaxAdditionalBatches, req.MaxAdditionalBatches > 0)
	if err != nil {
		return types.ReleaseDeferredRootChunksResponse{}, err
	}
	status, err := s.GetRootJobStatus(ctx, rootJobID)
	if err != nil {
		return types.ReleaseDeferredRootChunksResponse{}, err
	}
	return types.ReleaseDeferredRootChunksResponse{
		RootJobID:                     rootJobID,
		ReleasedChunks:                release.ReleasedChunks,
		ReleasedChildren:              release.ReleasedChildren,
		ReducerReleased:               release.ReducerReleased,
		CumulativeForcedReleaseChunks: status.ForcedReleasedChunks,
		RemainingForcedReleaseBudget:  remainingNonAdminBudget(s.options.RootActionMaxAdditionalBatches, status.ForcedReleasedChunks, auth.PrincipalFromContext(ctx)),
		RootStatus:                    status,
	}, nil
}

func (s *Service) CancelJob(ctx context.Context, jobID string) (types.CancelJobResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		s.auditDeniedLookup(ctx, "job.cancel", jobID, err)
		return types.CancelJobResponse{}, err
	}

	if job.BackendRunID != "" {
		if err := s.backend.CancelRun(ctx, job.BackendRunID); err != nil {
			return types.CancelJobResponse{}, fmt.Errorf("cancel backend run: %w", err)
		}
	}

	now := time.Now().UTC()
	job.State = types.JobStateCancelled
	job.CompletedAt = &now
	job.UpdatedAt = now

	if err := s.store.UpdateJob(ctx, job); err != nil {
		return types.CancelJobResponse{}, fmt.Errorf("update job: %w", err)
	}

	s.logger.Printf("cancelled job=%s backend_run_id=%s", job.ID, job.BackendRunID)
	s.audit(ctx, "job.cancel", "success", &job, nil)

	return types.CancelJobResponse{
		JobID: job.ID,
		State: job.State,
	}, nil
}

func (s *Service) GetJobLogs(ctx context.Context, jobID, stream string, maxBytes int) (types.JobLogs, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		s.auditDeniedLookup(ctx, "job.fetch_logs", jobID, err)
		return types.JobLogs{}, err
	}
	if err := policy.AuthorizeJobLogs(job); err != nil {
		s.audit(ctx, "job.fetch_logs", "policy_denied", &job, map[string]any{
			"stream": stream,
		})
		return types.JobLogs{}, err
	}

	if stream == "" {
		stream = "combined"
	}
	if maxBytes <= 0 {
		maxBytes = 16384
	}

	runDir := filepath.Join(s.runRoot, job.ID)
	stdoutPath := filepath.Join(runDir, "stdout.log")
	stderrPath := filepath.Join(runDir, "stderr.log")

	stdoutText, _ := readLogFile(stdoutPath)
	stderrText, _ := readLogFile(stderrPath)

	var content string
	var sourceRefs []string
	switch stream {
	case "stdout":
		content = stdoutText
		if stdoutText != "" {
			sourceRefs = append(sourceRefs, "stdout.log")
		}
	case "stderr":
		content = stderrText
		if stderrText != "" {
			sourceRefs = append(sourceRefs, "stderr.log")
		}
	case "combined":
		content, sourceRefs = combineLogs(stdoutText, stderrText)
	default:
		return types.JobLogs{}, fmt.Errorf("unsupported log stream: %s", stream)
	}

	content = redactLogContent(content)
	content, truncated := truncateLogContent(content, maxBytes)

	return types.JobLogs{
		JobID:      job.ID,
		State:      job.State,
		Stream:     stream,
		Content:    content,
		Truncated:  truncated,
		MaxBytes:   maxBytes,
		SourceRefs: sourceRefs,
	}, s.auditAndReturnLogs(ctx, job, stream, maxBytes)
}

func (s *Service) GetReleasedResult(ctx context.Context, jobID string) (types.JobResultRelease, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		s.auditDeniedLookup(ctx, "job.fetch_result", jobID, err)
		return types.JobResultRelease{}, err
	}

	result, artifacts, err := policy.FilterJobResult(job)
	if err != nil {
		return types.JobResultRelease{}, err
	}
	release := types.JobResultRelease{
		JobID:                  job.ID,
		State:                  job.State,
		Result:                 result,
		RuntimeDiagnostics:     cloneMap(job.RuntimeDiagnostics),
		ExecutionQuality:       job.ExecutionQuality,
		DegradedLocalExecution: job.DegradedLocalExecution,
		RetryRecommended:       job.RetryRecommended,
		Artifacts:              artifacts,
	}
	s.audit(ctx, "job.fetch_result", "success", &job, map[string]any{
		"artifact_count": len(artifacts),
		"has_result":     result != nil,
	})
	return release, nil
}

func (s *Service) GetJobRetryRecommendation(ctx context.Context, jobID string) (types.JobRetryRecommendation, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return types.JobRetryRecommendation{}, err
	}
	if job.Result == nil {
		return types.JobRetryRecommendation{}, fmt.Errorf("job %q has no result", jobID)
	}
	rec, ok := retryRecommendationFromResult(job)
	if !ok {
		return types.JobRetryRecommendation{}, fmt.Errorf("job %q has no broker retry recommendation", jobID)
	}
	return rec, nil
}

func (s *Service) RetryJobWithRecommendation(ctx context.Context, jobID string) (types.SubmitJobResponse, error) {
	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return types.SubmitJobResponse{}, err
	}
	if job.Result == nil {
		return types.SubmitJobResponse{}, fmt.Errorf("job %q has no result", jobID)
	}
	rec, ok := retryRecommendationFromResult(job)
	if !ok {
		return types.SubmitJobResponse{}, fmt.Errorf("job %q has no broker retry recommendation", jobID)
	}
	req := job.Request
	req.TaskParams = cloneTaskParams(job.Request.TaskParams)
	req.TaskParams["_broker_retry_recommended_of_job_id"] = job.ID
	req.ExecutionProfile = rec.ExecutionProfile
	req.ExecutionProfile = mergePlacementHintIntoProfile(req.ExecutionProfile, rec.PlacementHint)
	req.ExecutionProfile = s.applyExecutionProfileDefaults(req.ExecutionProfile)
	req.TaskParams = mergePlacementHintIntoTaskParams(req.TaskParams, rec.PlacementHint)
	req.IdempotencyKey = ""
	return s.SubmitJob(ctx, req)
}

func (s *Service) GetArtifactMetadata(ctx context.Context, artifactID string, allowedTypes map[string]struct{}) (types.ArtifactMetadata, error) {
	principal := auth.PrincipalFromContext(ctx)
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return types.ArtifactMetadata{}, err
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].SubmittedAt.After(jobs[j].SubmittedAt)
	})
	for _, job := range jobs {
		if err := authz.AuthorizeJobAccess(principal, job); err != nil {
			continue
		}
		for _, artifact := range job.Artifacts {
			if artifact.ArtifactID != artifactID {
				continue
			}
			if len(allowedTypes) > 0 {
				if _, ok := allowedTypes[artifact.ArtifactType]; !ok {
					continue
				}
			}
			schemaName := ""
			if job.Result != nil {
				schemaName = job.Result.SchemaName
			}
			return types.ArtifactMetadata{
				ArtifactID:     artifact.ArtifactID,
				ArtifactType:   artifact.ArtifactType,
				Classification: artifact.Classification,
				ContentHash:    artifact.ContentHash,
				SourceJobID:    job.ID,
				SourceTaskType: job.TaskType,
				SourceSchema:   schemaName,
				SubmittedBy:    job.SubmittedBy,
				CreatedAt:      job.CreatedAt.Format(time.RFC3339),
			}, nil
		}
	}
	return types.ArtifactMetadata{}, store.ErrNotFound
}

func (s *Service) LookupCache(ctx context.Context, req types.SubmitJobRequest) (types.CacheLookupResponse, error) {
	cacheKey, cacheable, err := cache.KeyForRequest(req)
	if err != nil {
		return types.CacheLookupResponse{}, fmt.Errorf("compute cache key: %w", err)
	}
	resp := types.CacheLookupResponse{
		Status:     "uncacheable",
		TaskType:   req.TaskType,
		SchemaName: req.OutputSchema.Name,
		CacheKey:   cacheKey,
	}
	if !cacheable {
		return resp, nil
	}
	resp.Status = "miss"
	cachedJob, err := cache.FindCompletedJobByCacheKey(ctx, s.store, cacheKey)
	if err != nil {
		return types.CacheLookupResponse{}, fmt.Errorf("lookup cache: %w", err)
	}
	if cachedJob == nil {
		return resp, nil
	}
	principal := auth.PrincipalFromContext(ctx)
	if err := authz.AuthorizeJobAccess(principal, *cachedJob); err != nil {
		// Do not disclose inaccessible cache hits to non-admin callers.
		return resp, nil
	}
	resp.Status = "hit"
	resp.SourceJobID = cachedJob.ID
	resp.ArtifactCount = len(cachedJob.Artifacts)
	return resp, nil
}

func newJobID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("job_%d", time.Now().UnixNano())
	}
	return "job_" + hex.EncodeToString(buf)
}

func newRootJobID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("root_%d", time.Now().UnixNano())
	}
	return "root_" + hex.EncodeToString(buf)
}

func isTerminal(state types.JobState) bool {
	switch state {
	case types.JobStateSucceeded, types.JobStateFailed, types.JobStateCancelled, types.JobStatePreempted, types.JobStateTimedOut:
		return true
	default:
		return false
	}
}

func (s *Service) refreshProgress(ctx context.Context, job types.Job) (types.Job, error) {
	heartbeatPath := filepath.Join(s.runRoot, job.ID, "heartbeat.json")
	heartbeatBytes, err := os.ReadFile(heartbeatPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return job, nil
		}
		return job, err
	}

	var heartbeat struct {
		JobID     string         `json:"job_id"`
		State     string         `json:"state"`
		Phase     string         `json:"phase"`
		Percent   int            `json:"percent"`
		Message   string         `json:"message"`
		Timestamp string         `json:"timestamp"`
		Metrics   map[string]any `json:"metrics"`
	}
	if err := json.Unmarshal(heartbeatBytes, &heartbeat); err != nil {
		return job, err
	}

	progress := &types.ProgressInfo{
		State:   heartbeat.State,
		Phase:   heartbeat.Phase,
		Percent: heartbeat.Percent,
		Message: heartbeat.Message,
		Metrics: heartbeat.Metrics,
	}
	now := time.Now().UTC()
	progress.LastUpdated = &now
	if heartbeat.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, heartbeat.Timestamp); err == nil {
			progress.Timestamp = &ts
			progress.LastUpdated = &ts
		}
	}

	if progressEquals(job.Progress, progress) {
		return job, nil
	}

	job.Progress = progress
	job.UpdatedAt = now
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return types.Job{}, fmt.Errorf("persist progress: %w", err)
	}
	return job, nil
}

func (s *Service) ingestRunOutputs(ctx context.Context, job types.Job) (types.Job, error) {
	resultPath := filepath.Join(s.runRoot, job.ID, "result.json")
	artifactsPath := filepath.Join(s.runRoot, job.ID, "artifacts.json")

	resultBytes, err := os.ReadFile(resultPath)
	if err != nil {
		return job, err
	}

	var result types.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return job, err
	}
	if err := schemas.ValidateResult(job.TaskType, job.Request.OutputSchema.Name, result); err != nil {
		return job, err
	}
	applyBrokerResultPolicies(&job, &result)
	job.Result = &result

	if artifactBytes, err := os.ReadFile(artifactsPath); err == nil && len(artifactBytes) > 0 {
		var artifacts []types.Artifact
		if err := json.Unmarshal(artifactBytes, &artifacts); err == nil {
			job.Artifacts = artifacts
		}
	}
	job.RuntimeDiagnostics = s.extractRuntimeDiagnostics(job, result)
	job.DegradedLocalExecution = isDegradedLocalExecution(job.RuntimeDiagnostics)
	job.RetryRecommended = hasRetryRecommendation(job)
	job.ExecutionQuality = deriveExecutionQuality(job.RuntimeDiagnostics, job.RetryRecommended)

	job.UpdatedAt = time.Now().UTC()
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return types.Job{}, fmt.Errorf("persist ingested outputs: %w", err)
	}
	return job, nil
}

func applyBrokerResultPolicies(job *types.Job, result *types.Result) {
	if job == nil || result == nil {
		return
	}
	if !isRAGLikeTask(job.TaskType) {
		return
	}
	payload := result.Payload
	policySignals, ok := payload["policy_signals"].(map[string]any)
	if !ok {
		return
	}

	warnings := collectStringSlice(policySignals["warnings"])
	if len(warnings) == 0 {
		return
	}
	existing := collectStringSlice(payload["warnings"])
	for _, warning := range warnings {
		switch warning {
		case "LOCAL_RETRIEVAL_DEGRADED":
			existing = appendUniqueString(existing, "broker_local_retrieval_degraded")
		case "NO_REAL_RETRIEVAL_BACKEND":
			existing = appendUniqueString(existing, "broker_no_real_retrieval_backend")
			payload["broker_retry_recommendation"] = brokerRetryRecommendation(*job)
			if job.ResultError == "" {
				job.ResultError = "broker_policy_no_real_retrieval_backend"
			}
		}
	}
	if len(existing) > 0 {
		payload["warnings"] = stringSliceToAny(existing)
	}
}

func (s *Service) extractRuntimeDiagnostics(job types.Job, result types.Result) map[string]any {
	if diagnostics := sanitizeRuntimeDiagnostics(artifactJSONForType(s.runRoot, job.ID, job.Artifacts, "runtime_diagnostics")); len(diagnostics) > 0 {
		return diagnostics
	}
	if diagnostics := sanitizeRuntimeDiagnostics(validationRuntimeDiagnostics(s.runRoot, job.ID, job.Artifacts)); len(diagnostics) > 0 {
		return diagnostics
	}
	if diagnostics := sanitizeRuntimeDiagnostics(runtimeDiagnosticsFromPayload(result.Payload)); len(diagnostics) > 0 {
		return diagnostics
	}
	return nil
}

func artifactJSONForType(runRoot, jobID string, artifacts []types.Artifact, artifactType string) map[string]any {
	for _, artifact := range artifacts {
		if artifact.ArtifactType != artifactType || strings.TrimSpace(artifact.Path) == "" {
			continue
		}
		var payload map[string]any
		if bytes, err := os.ReadFile(artifact.Path); err == nil && len(bytes) > 0 {
			if err := json.Unmarshal(bytes, &payload); err == nil {
				return payload
			}
		}
	}
	fallbackPath := filepath.Join(runRoot, jobID, artifactType+".json")
	var payload map[string]any
	if bytes, err := os.ReadFile(fallbackPath); err == nil && len(bytes) > 0 {
		if err := json.Unmarshal(bytes, &payload); err == nil {
			return payload
		}
	}
	return nil
}

func validationRuntimeDiagnostics(runRoot, jobID string, artifacts []types.Artifact) map[string]any {
	validation := artifactJSONForType(runRoot, jobID, artifacts, "validation_report")
	if len(validation) == 0 {
		return nil
	}
	diagnostics, _ := validation["runtime_diagnostics"].(map[string]any)
	return diagnostics
}

func runtimeDiagnosticsFromPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	retrieval, _ := payload["retrieval"].(map[string]any)
	provenance, _ := payload["provenance"].(map[string]any)
	if len(retrieval) == 0 && len(provenance) == 0 {
		return nil
	}
	diagnostics := map[string]any{}
	if value := strings.TrimSpace(stringValue(provenance["runtime_backend"])); value != "" {
		diagnostics["runtime_backend"] = value
		diagnostics["backend_name"] = value
	}
	if value := strings.TrimSpace(stringValue(provenance["model"])); value != "" {
		diagnostics["selected_model"] = value
		diagnostics["backend_detail"] = value
	}
	if value := strings.TrimSpace(stringValue(provenance["resource_tier"])); value != "" {
		diagnostics["resource_tier"] = value
	}
	if value := strings.TrimSpace(stringValue(retrieval["runtime_backend_mode"])); value != "" {
		diagnostics["backend_mode"] = value
	}
	if value := strings.TrimSpace(stringValue(retrieval["runtime_backend_detail"])); value != "" {
		diagnostics["backend_detail"] = value
	}
	return diagnostics
}

func sanitizeRuntimeDiagnostics(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := map[string]any{}
	for _, key := range []string{
		"runtime_backend",
		"selected_model",
		"resource_tier",
		"backend_name",
		"backend_mode",
		"backend_detail",
		"llm_available",
		"endpoint_configured",
		"timeout_seconds",
		"last_error",
	} {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				output[key] = typed
			}
		case bool:
			output[key] = typed
		case float64, int, int32, int64:
			output[key] = typed
		}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func isDegradedLocalExecution(diagnostics map[string]any) bool {
	mode := strings.ToLower(strings.TrimSpace(stringValue(diagnostics["backend_mode"])))
	if mode == "" || mode == "real" {
		return false
	}
	return true
}

func hasRetryRecommendation(job types.Job) bool {
	rec, ok := retryRecommendationFromResult(job)
	return ok && rec.Recommended
}

func deriveExecutionQuality(diagnostics map[string]any, retryRecommended bool) string {
	if retryRecommended {
		return "no_real_backend"
	}
	mode := strings.ToLower(strings.TrimSpace(stringValue(diagnostics["backend_mode"])))
	switch mode {
	case "real":
		return "real_local"
	case "heuristic", "fallback", "unavailable", "configured_local_llm":
		return "degraded_local"
	default:
		return ""
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func retryRecommendationFromResult(job types.Job) (types.JobRetryRecommendation, bool) {
	if job.Result == nil {
		return types.JobRetryRecommendation{}, false
	}
	raw, ok := job.Result.Payload["broker_retry_recommendation"].(map[string]any)
	if !ok {
		return types.JobRetryRecommendation{}, false
	}
	profileMap, ok := raw["execution_profile"].(map[string]any)
	if !ok {
		return types.JobRetryRecommendation{}, false
	}
	return types.JobRetryRecommendation{
		JobID:             job.ID,
		Recommended:       raw["recommended"] == true,
		Reason:            stringValue(raw["reason"]),
		TaskType:          stringValue(raw["task_type"]),
		ExecutionProfile:  executionProfileFromMap(profileMap),
		PlacementHint:     placementHintFromMap(mapValue(raw["placement_hint"])),
		SourceResultError: job.ResultError,
	}, true
}

func brokerRetryRecommendation(job types.Job) map[string]any {
	profile := recommendedExecutionProfile(job.Request.ExecutionProfile)
	placement := recommendedPlacementHint(job, profile)
	return map[string]any{
		"recommended":       true,
		"reason":            "no_real_retrieval_backend",
		"task_type":         job.TaskType,
		"execution_profile": profile,
		"placement_hint":    placement,
	}
}

func executionProfileFromMap(value map[string]any) types.ExecutionProfile {
	return types.ExecutionProfile{
		Backend:    stringValue(value["backend"]),
		Tier:       stringValue(value["tier"]),
		Runtime:    stringValue(value["runtime"]),
		Model:      stringValue(value["model"]),
		QOS:        stringValue(value["qos"]),
		NodeList:   stringValue(value["nodelist"]),
		Constraint: stringValue(value["constraint"]),
	}
}

func placementHintFromMap(value map[string]any) types.PlacementHint {
	return types.PlacementHint{
		BackendPreference: stringValue(value["backend_preference"]),
		TierPreference:    stringValue(value["tier_preference"]),
		QOS:               stringValue(value["qos"]),
		NodeList:          stringValue(value["nodelist"]),
		Constraint:        stringValue(value["constraint"]),
		Preemptible:       boolValue(value["preemptible"]),
		Rationale:         stringValue(value["rationale"]),
	}
}

func recommendedPlacementHint(job types.Job, profile map[string]any) map[string]any {
	tier := stringValue(profile["tier"])
	hint := map[string]any{
		"backend_preference": stringValue(profile["backend"]),
		"tier_preference":    tier,
		"preemptible":        true,
	}
	if value := firstNonEmpty(stringValue(profile["nodelist"]), job.Request.ExecutionProfile.NodeList); value != "" {
		hint["nodelist"] = value
	}
	if value := firstNonEmpty(stringValue(profile["constraint"]), job.Request.ExecutionProfile.Constraint); value != "" {
		hint["constraint"] = value
	}
	switch tier {
	case "p40-rag-compression":
		hint["qos"] = firstNonEmpty(job.Request.ExecutionProfile.QOS, "scavenger")
		hint["rationale"] = "Prefer low-contention P40 compression capacity before escalating to premium accelerators."
	case "a100-reasoning":
		hint["qos"] = firstNonEmpty(job.Request.ExecutionProfile.QOS, "scavenger")
		hint["rationale"] = "Escalate to A100 only after lower-cost local retrieval paths failed to produce a real backend result."
	default:
		hint["qos"] = firstNonEmpty(job.Request.ExecutionProfile.QOS, "normal")
		hint["rationale"] = "Use the broker-recommended local tier with non-blocking placement."
	}
	return hint
}

func mergePlacementHintIntoProfile(profile types.ExecutionProfile, hint types.PlacementHint) types.ExecutionProfile {
	if strings.TrimSpace(hint.BackendPreference) != "" {
		profile.Backend = hint.BackendPreference
	}
	if strings.TrimSpace(hint.TierPreference) != "" {
		profile.Tier = hint.TierPreference
	}
	if strings.TrimSpace(hint.QOS) != "" {
		profile.QOS = hint.QOS
	}
	if strings.TrimSpace(hint.NodeList) != "" {
		profile.NodeList = hint.NodeList
	}
	if strings.TrimSpace(hint.Constraint) != "" {
		profile.Constraint = hint.Constraint
	}
	return profile
}

func mergePlacementHintIntoTaskParams(taskParams map[string]any, hint types.PlacementHint) map[string]any {
	if taskParams == nil {
		taskParams = make(map[string]any)
	}
	if strings.TrimSpace(hint.BackendPreference) != "" {
		taskParams["_broker_retry_backend_preference"] = hint.BackendPreference
	}
	if strings.TrimSpace(hint.TierPreference) != "" {
		taskParams["_broker_retry_tier_preference"] = hint.TierPreference
	}
	if strings.TrimSpace(hint.QOS) != "" {
		taskParams["_broker_retry_qos"] = hint.QOS
	}
	if strings.TrimSpace(hint.NodeList) != "" {
		taskParams["_broker_retry_nodelist"] = hint.NodeList
	}
	if strings.TrimSpace(hint.Constraint) != "" {
		taskParams["_broker_retry_constraint"] = hint.Constraint
	}
	taskParams["_broker_retry_preemptible"] = hint.Preemptible
	if strings.TrimSpace(hint.Rationale) != "" {
		taskParams["_broker_retry_rationale"] = hint.Rationale
	}
	return taskParams
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func mapValue(value any) map[string]any {
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{}
}

func boolValue(value any) bool {
	if out, ok := value.(bool); ok {
		return out
	}
	return false
}

func recommendedExecutionProfile(current types.ExecutionProfile) map[string]any {
	tier := strings.TrimSpace(current.Tier)
	nextTier := "p40-rag-compression"
	switch tier {
	case "cpu-rag-indexing":
		nextTier = "p40-rag-compression"
	case "p40-rag-compression":
		nextTier = "a100-reasoning"
	case "a100-reasoning":
		nextTier = "a100-reasoning"
	}
	backend := strings.TrimSpace(current.Backend)
	if backend == "" {
		backend = "slurm"
	}
	runtime := strings.TrimSpace(current.Runtime)
	if runtime == "" {
		runtime = "llama.cpp"
	}
	return map[string]any{
		"backend":    backend,
		"tier":       nextTier,
		"runtime":    runtime,
		"nodelist":   strings.TrimSpace(current.NodeList),
		"constraint": strings.TrimSpace(current.Constraint),
	}
}

func isRAGLikeTask(taskType string) bool {
	switch taskType {
	case "rag_compress", "debug_with_local_context", "summarize_logs", "inspect_repo", "propose_patch":
		return true
	default:
		return false
	}
}

func collectStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func stringSliceToAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func (s *Service) stageExecutionBundle(ctx context.Context, job types.Job) error {
	jobDir := filepath.Join(s.runRoot, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return err
	}

	jobSpecPath := filepath.Join(jobDir, "job_spec.json")
	executionPlanPath := filepath.Join(jobDir, "execution_plan.json")
	inputManifestPath := filepath.Join(jobDir, "input_manifest.json")

	if err := writeJSONFile(jobSpecPath, map[string]any{
		"job_id":        job.ID,
		"task_type":     job.TaskType,
		"task_params":   job.Request.TaskParams,
		"output_schema": job.Request.OutputSchema,
		"constraints":   job.Request.Constraints,
	}); err != nil {
		return err
	}
	if err := writeJSONFile(executionPlanPath, map[string]any{
		"job_id":             job.ID,
		"task_type":          job.TaskType,
		"execution_profile":  job.Request.ExecutionProfile,
		"selected_model":     job.Request.ExecutionProfile.Model,
		"runtime_backend":    job.Request.ExecutionProfile.Runtime,
		"resource_tier":      job.Request.ExecutionProfile.Tier,
		"runtime_connection": s.runtimeConnectionPlan(job.Request.ExecutionProfile),
	}); err != nil {
		return err
	}

	resolvedInputRefs, err := s.resolveInputRefs(ctx, job)
	if err != nil {
		return err
	}

	if err := writeJSONFile(inputManifestPath, map[string]any{
		"job_id":     job.ID,
		"input_refs": resolvedInputRefs,
	}); err != nil {
		return err
	}

	return nil
}

func defaultModelProfiles() modelProfiles {
	return modelProfiles{
		p40:  "gpt-oss-20b.p40",
		a100: "qwen3-coder-30b.a100",
	}
}

func defaultRuntimeProfiles() runtimeProfiles {
	return runtimeProfiles{
		llamaCPP: runtimeConnection{TimeoutSeconds: 20},
		vllm:     runtimeConnection{TimeoutSeconds: 20},
		sglang:   runtimeConnection{TimeoutSeconds: 20},
	}
}

func (s *Service) applyExecutionProfileDefaults(profile types.ExecutionProfile) types.ExecutionProfile {
	switch strings.TrimSpace(profile.Tier) {
	case "cpu-rag-indexing":
		if strings.TrimSpace(profile.Runtime) == "" {
			profile.Runtime = "deterministic"
		}
		if strings.TrimSpace(profile.Model) == "" {
			profile.Model = s.models.cpu
		}
	case "p40-rag-compression":
		if strings.TrimSpace(profile.Runtime) == "" {
			profile.Runtime = "llama.cpp"
		}
		if strings.TrimSpace(profile.Model) == "" {
			profile.Model = s.models.p40
		}
	case "a100-reasoning":
		if strings.TrimSpace(profile.Runtime) == "" {
			profile.Runtime = "llama.cpp"
		}
		if strings.TrimSpace(profile.Model) == "" {
			profile.Model = s.models.a100
		}
	}
	return profile
}

func (s *Service) runtimeConnectionPlan(profile types.ExecutionProfile) map[string]any {
	runtimeName := strings.TrimSpace(profile.Runtime)
	connection := runtimeConnection{}
	switch runtimeName {
	case "llama.cpp":
		connection = s.runtimes.llamaCPP
	case "vllm":
		connection = s.runtimes.vllm
	case "sglang":
		connection = s.runtimes.sglang
	}
	return map[string]any{
		"base_url":        strings.TrimSpace(connection.BaseURL),
		"timeout_seconds": connection.TimeoutSeconds,
	}
}

func (s *Service) resolveInputRefs(ctx context.Context, job types.Job) ([]types.InputRef, error) {
	if len(job.Request.InputRefs) == 0 {
		return nil, nil
	}
	principal := auth.PrincipalFromContext(ctx)
	resolved := make([]types.InputRef, 0, len(job.Request.InputRefs))
	for _, input := range job.Request.InputRefs {
		cloned := input
		if !isArtifactInputRef(input) {
			resolved = append(resolved, cloned)
			continue
		}
		artifactID := strings.TrimSpace(strings.TrimPrefix(input.URI, "artifact://"))
		if artifactID == "" {
			return nil, fmt.Errorf("artifact input uri %q is missing an artifact id", input.URI)
		}
		meta, err := s.resolveArtifactRef(ctx, principal, job, artifactID)
		if err != nil {
			return nil, err
		}
		cloned.Metadata = mergeMetadata(cloned.Metadata, meta)
		resolved = append(resolved, cloned)
	}
	return resolved, nil
}

func (s *Service) resolveArtifactRef(ctx context.Context, principal auth.Principal, requestingJob types.Job, artifactID string) (map[string]any, error) {
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list jobs for artifact resolution: %w", err)
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].SubmittedAt.After(jobs[j].SubmittedAt)
	})
	for _, candidate := range jobs {
		if candidate.ID == requestingJob.ID {
			continue
		}
		if !artifactJobAccessible(principal, requestingJob, candidate) {
			continue
		}
		for _, artifact := range candidate.Artifacts {
			if artifact.ArtifactID != artifactID {
				continue
			}
			resolvedPath := resolveArtifactPath(s.runRoot, candidate.ID, artifact.Path)
			if resolvedPath != "" {
				if _, statErr := os.Stat(resolvedPath); statErr != nil {
					return nil, fmt.Errorf("artifact %s path %q is unavailable: %w", artifactID, resolvedPath, statErr)
				}
			}
			return map[string]any{
				"artifact_id":        artifact.ArtifactID,
				"artifact_type":      artifact.ArtifactType,
				"source_job_id":      candidate.ID,
				"resolved_path":      resolvedPath,
				"classification":     firstNonEmpty(artifact.Classification, requestingJob.Request.Constraints.Confidentiality),
				"source_result_name": resultSchemaName(candidate.Result),
			}, nil
		}
	}
	return nil, fmt.Errorf("artifact %s not found in accessible broker jobs", artifactID)
}

func artifactJobAccessible(principal auth.Principal, requestingJob, candidate types.Job) bool {
	if auth.IsAdmin(principal) {
		return true
	}
	if requestingJob.SubmittedBy != "" && candidate.SubmittedBy != "" {
		return requestingJob.SubmittedBy == candidate.SubmittedBy
	}
	if principal.Actor != "" {
		if candidate.SubmittedBy == "" {
			return true
		}
		return principal.Actor == candidate.SubmittedBy
	}
	return candidate.SubmittedBy == ""
}

func resolveArtifactPath(runRoot, jobID, artifactPath string) string {
	if strings.TrimSpace(artifactPath) == "" {
		return ""
	}
	if filepath.IsAbs(artifactPath) {
		return artifactPath
	}
	return filepath.Join(runRoot, jobID, artifactPath)
}

func isArtifactInputRef(input types.InputRef) bool {
	return input.Type == "artifact" || strings.HasPrefix(input.URI, "artifact://")
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func resultSchemaName(result *types.Result) string {
	if result == nil {
		return ""
	}
	return result.SchemaName
}

func writeJSONFile(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func cloneTaskParams(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cloneArtifacts(in []types.Artifact) []types.Artifact {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Artifact, len(in))
	copy(out, in)
	return out
}

func compactNonEmptyStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out
}

func normalizeOptions(opts Options) Options {
	if opts.ParallelMaxBatchSize <= 0 {
		opts.ParallelMaxBatchSize = 64
	}
	if opts.ParallelMaxActiveBatches < 0 {
		opts.ParallelMaxActiveBatches = 0
	}
	if opts.RootActionMaxAdditionalBatches <= 0 {
		opts.RootActionMaxAdditionalBatches = 1
	}
	if opts.RootActionMaxRetriedShards <= 0 {
		opts.RootActionMaxRetriedShards = 4
	}
	return opts
}

func retrySubmitRequest(job types.Job) types.SubmitJobRequest {
	req := job.Request
	req.TaskParams = cloneTaskParams(job.Request.TaskParams)
	req.Orchestration = types.OrchestrationRequest{
		ParentJobID:     job.ParentJobID,
		RootJobID:       job.RootJobID,
		Strategy:        orchestrationStrategyOf(job),
		ShardKey:        shardKeyOf(job),
		ShardIndex:      shardIndexOf(job),
		ShardCount:      shardCountOf(job),
		AggregationKey:  aggregationKeyOf(job),
		DependsOnJobIDs: dependsOnJobIDsOf(job),
	}
	return req
}

func (s *Service) submitRetriedReducer(ctx context.Context, reducer types.Job, effectiveChildren []types.Job) (*types.SubmitJobResponse, error) {
	childJobIDs := make([]string, 0, len(effectiveChildren))
	childBackendRunIDs := make([]string, 0, len(effectiveChildren))
	for _, child := range effectiveChildren {
		childJobIDs = append(childJobIDs, child.ID)
		if child.BackendRunID != "" {
			childBackendRunIDs = append(childBackendRunIDs, child.BackendRunID)
		}
	}

	reducerReq := retrySubmitRequest(reducer)
	reducerReq.TaskParams["child_job_ids"] = append([]string(nil), childJobIDs...)
	reducerReq.TaskParams["root_job_id"] = reducer.RootJobID
	reducerReq.TaskParams["_dependency_backend_run_ids"] = append([]string(nil), childBackendRunIDs...)

	resp, err := s.SubmitJob(ctx, reducerReq)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) submitStoredReducer(ctx context.Context, reducer types.Job, effectiveChildren []types.Job) error {
	childJobIDs := make([]string, 0, len(effectiveChildren))
	childBackendRunIDs := make([]string, 0, len(effectiveChildren))
	for _, child := range effectiveChildren {
		childJobIDs = append(childJobIDs, child.ID)
		if child.BackendRunID != "" {
			childBackendRunIDs = append(childBackendRunIDs, child.BackendRunID)
		}
	}

	reducer.Request.TaskParams = cloneTaskParams(reducer.Request.TaskParams)
	reducer.Request.TaskParams["child_job_ids"] = append([]string(nil), childJobIDs...)
	reducer.Request.TaskParams["root_job_id"] = reducer.RootJobID
	reducer.Request.TaskParams["_dependency_backend_run_ids"] = append([]string(nil), childBackendRunIDs...)
	if err := s.stageExecutionBundle(ctx, reducer); err != nil {
		return err
	}
	submitResp, err := s.backend.SubmitRun(ctx, reducer)
	if err != nil {
		return err
	}
	reducer.State = submitResp.InitialState
	reducer.BackendKind = submitResp.BackendKind
	reducer.BackendRunID = submitResp.BackendRunID
	reducer.UpdatedAt = time.Now().UTC()
	return s.store.UpdateJob(ctx, reducer)
}

func (s *Service) createDeferredReducer(ctx context.Context, req types.SubmitJobRequest) (*types.SubmitJobResponse, error) {
	now := time.Now().UTC()
	principal := auth.PrincipalFromContext(ctx)
	jobID := newJobID()
	orchestration := normalizeOrchestration(jobID, req.Orchestration)
	job := types.Job{
		ID:            jobID,
		TaskType:      req.TaskType,
		State:         types.JobStateDispatching,
		SubmittedBy:   principal.Actor,
		Request:       req,
		CreatedAt:     now,
		UpdatedAt:     now,
		SubmittedAt:   now,
		ParentJobID:   orchestration.ParentJobID,
		RootJobID:     orchestration.RootJobID,
		Orchestration: orchestration,
	}
	if err := s.stageExecutionBundle(ctx, job); err != nil {
		return nil, err
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	s.audit(ctx, "job.submit", "success", &job, map[string]any{
		"backend_kind": job.BackendKind,
	})
	resp := types.SubmitJobResponse{
		JobID:     job.ID,
		State:     job.State,
		Cache:     types.CacheStatus{Status: job.CacheStatus},
		StatusURL: "/v1/jobs/" + job.ID,
	}
	return &resp, nil
}

type dispatchReleaseResult struct {
	ReleasedChunks   int
	ReleasedChildren int
	ReducerReleased  bool
}

func (s *Service) releaseDispatchingRootChildren(ctx context.Context, rootJobID string, maxAdditionalBatches int, forced bool) (dispatchReleaseResult, error) {
	result := dispatchReleaseResult{}
	if strings.TrimSpace(rootJobID) == "" {
		return result, nil
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return result, err
	}
	rootJobs := make([]types.Job, 0, len(jobs))
	for _, job := range jobs {
		if job.RootJobID == rootJobID {
			rootJobs = append(rootJobs, job)
		}
	}
	if len(rootJobs) == 0 {
		return result, nil
	}
	if err := s.refreshRootJobsForDispatch(ctx, rootJobs); err != nil {
		return result, err
	}
	jobs, err = s.store.ListJobs(ctx)
	if err != nil {
		return result, err
	}
	rootJobs = rootJobs[:0]
	for _, job := range jobs {
		if job.RootJobID == rootJobID {
			rootJobs = append(rootJobs, job)
		}
	}

	activeChunks := activeDispatchChunks(rootJobs)
	slots := availableRootBatchSlots(s.options.ParallelMaxActiveBatches, len(activeChunks))
	if maxAdditionalBatches > 0 {
		slots = maxAdditionalBatches
	}
	if slots > 0 {
		dispatchingChildren := dispatchingChildrenByChunk(rootJobs)
		chunkIndexes := sortedChunkIndexes(dispatchingChildren)
		for _, chunkIndex := range chunkIndexes {
			if slots == 0 {
				break
			}
			chunkJobs := dispatchingChildren[chunkIndex]
			if len(chunkJobs) == 0 {
				continue
			}
			if err := s.submitStoredChunk(ctx, chunkJobs, forced); err != nil {
				return result, err
			}
			result.ReleasedChunks++
			result.ReleasedChildren += len(chunkJobs)
			slots--
		}
	}

	if hasDispatchingChildrenForJobs(rootJobs) {
		return result, nil
	}

	reducers := effectiveReducerJobs(rootJobs)
	for _, reducer := range reducers {
		if reducer.State == types.JobStateDispatching && reducer.BackendRunID == "" {
			effectiveChildren := effectiveShardJobs(rootJobs, false)
			if err := s.submitStoredReducer(ctx, reducer, effectiveChildren); err != nil {
				return result, err
			}
			result.ReducerReleased = true
			return result, nil
		}
	}
	return result, nil
}

func (s *Service) refreshRootJobsForDispatch(ctx context.Context, jobs []types.Job) error {
	for _, job := range jobs {
		if job.BackendRunID == "" || isTerminal(job.State) {
			continue
		}
		runStatus, err := s.backend.GetRun(ctx, job.BackendRunID)
		if err != nil || runStatus.State == "" || runStatus.State == job.State {
			continue
		}
		job.State = runStatus.State
		job.BackendState = runStatus.RawState
		job.BackendExitCode = runStatus.ExitCode
		now := time.Now().UTC()
		job.UpdatedAt = now
		if runStatus.State == types.JobStateRunning && job.StartedAt == nil {
			job.StartedAt = &now
		}
		if isTerminal(runStatus.State) {
			job.CompletedAt = &now
		}
		if err := s.store.UpdateJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) submitStoredChunk(ctx context.Context, chunkJobs []types.Job, forced bool) error {
	if len(chunkJobs) == 0 {
		return nil
	}
	if len(chunkJobs) == 1 {
		if forced {
			chunkJobs[0].Request.TaskParams = cloneTaskParams(chunkJobs[0].Request.TaskParams)
			chunkJobs[0].Request.TaskParams["_broker_forced_release"] = true
		}
		submitResp, err := s.backend.SubmitRun(ctx, chunkJobs[0])
		if err != nil {
			return fmt.Errorf("submit child shard %d: %w", shardIndexOf(chunkJobs[0]), err)
		}
		return s.applyStoredSubmission(ctx, chunkJobs[0], submitResp)
	}
	if batchBackend, ok := s.backend.(backends.BatchBackend); ok {
		if forced {
			for i := range chunkJobs {
				chunkJobs[i].Request.TaskParams = cloneTaskParams(chunkJobs[i].Request.TaskParams)
				chunkJobs[i].Request.TaskParams["_broker_forced_release"] = true
			}
		}
		submitResps, err := batchBackend.SubmitRunBatch(ctx, chunkJobs)
		if err != nil {
			return fmt.Errorf("submit child batch chunk %d: %w", dispatchChunkIndex(chunkJobs[0]), err)
		}
		if len(submitResps) != len(chunkJobs) {
			return fmt.Errorf("submit child batch chunk %d: expected %d responses, got %d", dispatchChunkIndex(chunkJobs[0]), len(chunkJobs), len(submitResps))
		}
		for i := range chunkJobs {
			if err := s.applyStoredSubmission(ctx, chunkJobs[i], submitResps[i]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, job := range chunkJobs {
		if forced {
			job.Request.TaskParams = cloneTaskParams(job.Request.TaskParams)
			job.Request.TaskParams["_broker_forced_release"] = true
		}
		submitResp, err := s.backend.SubmitRun(ctx, job)
		if err != nil {
			return fmt.Errorf("submit child shard %d: %w", shardIndexOf(job), err)
		}
		if err := s.applyStoredSubmission(ctx, job, submitResp); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyStoredSubmission(ctx context.Context, job types.Job, submitResp backends.SubmitResponse) error {
	job.State = submitResp.InitialState
	job.BackendKind = submitResp.BackendKind
	job.BackendRunID = submitResp.BackendRunID
	job.UpdatedAt = time.Now().UTC()
	return s.store.UpdateJob(ctx, job)
}

func effectiveShardJobs(jobs []types.Job, includeCancelled bool) []types.Job {
	grouped := make(map[string][]types.Job)
	for _, job := range jobs {
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		grouped[shardAttemptKey(job)] = append(grouped[shardAttemptKey(job)], job)
	}
	effective := make([]types.Job, 0, len(grouped))
	for _, attempts := range grouped {
		effective = append(effective, selectEffectiveAttempt(attempts, includeCancelled))
	}
	sort.SliceStable(effective, func(i, j int) bool {
		if shardIndexOf(effective[i]) != shardIndexOf(effective[j]) {
			return shardIndexOf(effective[i]) < shardIndexOf(effective[j])
		}
		if shardKeyOf(effective[i]) != shardKeyOf(effective[j]) {
			return shardKeyOf(effective[i]) < shardKeyOf(effective[j])
		}
		return jobMoreRecent(effective[i], effective[j])
	})
	return effective
}

func effectiveReducerJobs(jobs []types.Job) []types.Job {
	grouped := make(map[string][]types.Job)
	for _, job := range jobs {
		if orchestrationStrategyOf(job) != "aggregator" {
			continue
		}
		grouped[reducerAttemptKey(job)] = append(grouped[reducerAttemptKey(job)], job)
	}
	effective := make([]types.Job, 0, len(grouped))
	for _, attempts := range grouped {
		effective = append(effective, selectEffectiveAttempt(attempts, false))
	}
	sort.SliceStable(effective, func(i, j int) bool {
		if aggregationKeyOf(effective[i]) != aggregationKeyOf(effective[j]) {
			return aggregationKeyOf(effective[i]) < aggregationKeyOf(effective[j])
		}
		return jobMoreRecent(effective[i], effective[j])
	})
	return effective
}

func activeDispatchChunks(jobs []types.Job) map[int]struct{} {
	active := make(map[int]struct{})
	for _, job := range jobs {
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		if job.BackendRunID == "" {
			continue
		}
		if isTerminal(job.State) {
			continue
		}
		active[dispatchChunkIndex(job)] = struct{}{}
	}
	return active
}

func dispatchObservability(jobs []types.Job) (dispatchingChildren, pendingChildren, activeChunks, pendingChunks int) {
	active := activeDispatchChunks(jobs)
	pending := dispatchingChildrenByChunk(jobs)
	for _, job := range jobs {
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		if job.State == types.JobStateDispatching {
			dispatchingChildren++
			if job.BackendRunID == "" {
				pendingChildren++
			}
		}
	}
	return dispatchingChildren, pendingChildren, len(active), len(pending)
}

type rootActionUsageSummary struct {
	ForcedReleasedChunks int
	RetriedShardActions  int
}

func rootActionUsage(jobs []types.Job) rootActionUsageSummary {
	usage := rootActionUsageSummary{}
	forcedChunks := make(map[int]struct{})
	for _, job := range jobs {
		if taskParamBool(job, "_broker_retry_action") {
			usage.RetriedShardActions++
		}
		if taskParamBool(job, "_broker_forced_release") {
			forcedChunks[dispatchChunkIndex(job)] = struct{}{}
		}
	}
	usage.ForcedReleasedChunks = len(forcedChunks)
	return usage
}

func availableRootBatchSlots(maxActiveBatches, currentActive int) int {
	if maxActiveBatches <= 0 {
		return int(^uint(0) >> 1)
	}
	if currentActive >= maxActiveBatches {
		return 0
	}
	return maxActiveBatches - currentActive
}

func dispatchingChildrenByChunk(jobs []types.Job) map[int][]types.Job {
	grouped := make(map[int][]types.Job)
	for _, job := range jobs {
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		if job.State != types.JobStateDispatching || job.BackendRunID != "" {
			continue
		}
		grouped[dispatchChunkIndex(job)] = append(grouped[dispatchChunkIndex(job)], job)
	}
	return grouped
}

func sortedChunkIndexes(grouped map[int][]types.Job) []int {
	indexes := make([]int, 0, len(grouped))
	for index := range grouped {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func hasDispatchingChildrenForJobs(jobs []types.Job) bool {
	for _, job := range jobs {
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		if job.State == types.JobStateDispatching && job.BackendRunID == "" {
			return true
		}
	}
	return false
}

func hasDispatchingChildrenForJobIDs(ctx context.Context, jobStore store.JobStore, jobIDs []string) bool {
	for _, jobID := range jobIDs {
		job, err := jobStore.GetJob(ctx, jobID)
		if err != nil {
			continue
		}
		if orchestrationStrategyOf(job) == "aggregator" {
			continue
		}
		if job.State == types.JobStateDispatching && job.BackendRunID == "" {
			return true
		}
	}
	return false
}

func selectEffectiveAttempt(attempts []types.Job, includeCancelled bool) types.Job {
	ordered := append([]types.Job(nil), attempts...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return jobMoreRecent(ordered[i], ordered[j])
	})
	for _, job := range ordered {
		if !isTerminal(job.State) {
			return job
		}
	}
	for _, job := range ordered {
		if job.State == types.JobStateSucceeded {
			return job
		}
	}
	for _, job := range ordered {
		if shouldRetryShard(job, includeCancelled) || job.State == types.JobStateCancelled {
			return job
		}
	}
	return ordered[0]
}

func shouldRetryShard(job types.Job, includeCancelled bool) bool {
	switch job.State {
	case types.JobStateFailed, types.JobStatePreempted, types.JobStateTimedOut:
		return true
	case types.JobStateCancelled:
		return includeCancelled
	default:
		return false
	}
}

func retrySkipReason(job types.Job, includeCancelled bool) string {
	switch {
	case !isTerminal(job.State):
		return "in_progress"
	case job.State == types.JobStateSucceeded:
		return "already_succeeded"
	case job.State == types.JobStateCancelled && !includeCancelled:
		return "cancelled_excluded"
	case shouldRetryShard(job, includeCancelled):
		return "not_retried"
	default:
		return "not_retryable"
	}
}

func dispatchChunkIndex(job types.Job) int {
	if job.Request.TaskParams == nil {
		return 0
	}
	switch typed := job.Request.TaskParams["_broker_dispatch_chunk"].(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func taskParamBool(job types.Job, key string) bool {
	if job.Request.TaskParams == nil {
		return false
	}
	value, ok := job.Request.TaskParams[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	return ok && typed
}

func shardAttemptKey(job types.Job) string {
	return strings.Join([]string{
		job.TaskType,
		orchestrationStrategyOf(job),
		shardKeyOf(job),
		fmt.Sprintf("%d", shardIndexOf(job)),
		fmt.Sprintf("%d", shardCountOf(job)),
		aggregationKeyOf(job),
	}, "|")
}

func reducerAttemptKey(job types.Job) string {
	return strings.Join([]string{
		job.TaskType,
		orchestrationStrategyOf(job),
		aggregationKeyOf(job),
	}, "|")
}

func orchestrationStrategyOf(job types.Job) string {
	if job.Orchestration == nil {
		return ""
	}
	return strings.TrimSpace(job.Orchestration.Strategy)
}

func shardKeyOf(job types.Job) string {
	if job.Orchestration == nil {
		return ""
	}
	return strings.TrimSpace(job.Orchestration.ShardKey)
}

func shardIndexOf(job types.Job) int {
	if job.Orchestration == nil {
		return 0
	}
	return job.Orchestration.ShardIndex
}

func shardCountOf(job types.Job) int {
	if job.Orchestration == nil {
		return 0
	}
	return job.Orchestration.ShardCount
}

func aggregationKeyOf(job types.Job) string {
	if job.Orchestration == nil {
		return ""
	}
	return strings.TrimSpace(job.Orchestration.AggregationKey)
}

func dependsOnJobIDsOf(job types.Job) []string {
	if job.Orchestration == nil {
		return nil
	}
	return append([]string(nil), job.Orchestration.DependsOnJobIDs...)
}

func jobMoreRecent(a, b types.Job) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func appendIfNonEmpty(in []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return in
	}
	return append(in, strings.TrimSpace(value))
}

func remainingNonAdminBudget(limit, used int, principal auth.Principal) int {
	if auth.IsAdmin(principal) {
		return 0
	}
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *Service) audit(ctx context.Context, action, outcome string, job *types.Job, fields map[string]any) {
	principal := auth.PrincipalFromContext(ctx)
	event := audit.Event{
		Actor:   principal.Actor,
		Role:    principal.Role,
		Action:  action,
		Outcome: outcome,
		Fields:  fields,
	}
	if job != nil {
		event.JobID = job.ID
		event.TaskType = job.TaskType
	}
	_ = s.auditLogger.Log(ctx, event)
}

func (s *Service) auditDeniedLookup(ctx context.Context, action, jobID string, err error) {
	outcome := "error"
	if errors.Is(err, authz.ErrForbidden) {
		outcome = "forbidden"
	} else if errors.Is(err, store.ErrNotFound) {
		outcome = "not_found"
	}
	s.audit(ctx, action, outcome, &types.Job{ID: jobID}, map[string]any{
		"error": err.Error(),
	})
}

func (s *Service) auditAndReturnLogs(ctx context.Context, job types.Job, stream string, maxBytes int) error {
	s.audit(ctx, "job.fetch_logs", "success", &job, map[string]any{
		"stream":    stream,
		"max_bytes": maxBytes,
	})
	return nil
}

func readLogFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func combineLogs(stdoutText, stderrText string) (string, []string) {
	parts := make([]string, 0, 2)
	sourceRefs := make([]string, 0, 2)
	if stdoutText != "" {
		parts = append(parts, "== stdout ==\n"+strings.TrimRight(stdoutText, "\n"))
		sourceRefs = append(sourceRefs, "stdout.log")
	}
	if stderrText != "" {
		parts = append(parts, "== stderr ==\n"+strings.TrimRight(stderrText, "\n"))
		sourceRefs = append(sourceRefs, "stderr.log")
	}
	return strings.Join(parts, "\n\n"), sourceRefs
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9._-]+)`),
	regexp.MustCompile(`(?i)(token=)([^&\s]+)`),
	regexp.MustCompile(`(?i)(api[_-]?key=)([^&\s]+)`),
}

func redactLogContent(content string) string {
	redacted := content
	for _, pattern := range secretPatterns {
		redacted = pattern.ReplaceAllString(redacted, `${1}[REDACTED]`)
	}
	return redacted
}

func truncateLogContent(content string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content, false
	}
	const suffix = "\n[TRUNCATED]\n"
	if maxBytes <= len(suffix) {
		return suffix[:maxBytes], true
	}
	return content[:maxBytes-len(suffix)] + suffix, true
}

func progressEquals(a, b *types.ProgressInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.State != b.State || a.Phase != b.Phase || a.Percent != b.Percent || a.Message != b.Message {
		return false
	}
	if !timePtrEqual(a.Timestamp, b.Timestamp) {
		return false
	}
	aMetrics, _ := json.Marshal(a.Metrics)
	bMetrics, _ := json.Marshal(b.Metrics)
	return string(aMetrics) == string(bMetrics)
}

func progressNewer(current, previous *types.ProgressInfo) bool {
	if current == nil {
		return false
	}
	if previous == nil {
		return true
	}
	if current.LastUpdated != nil && previous.LastUpdated != nil {
		return current.LastUpdated.After(*previous.LastUpdated)
	}
	if current.Timestamp != nil && previous.Timestamp != nil {
		return current.Timestamp.After(*previous.Timestamp)
	}
	return false
}

func setKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func aggregateRootState(status types.RootJobStatus) types.JobState {
	if status.ReducerJobID != "" && status.ReducerState != "" {
		return status.ReducerState
	}
	if status.RunningJobs > 0 {
		return types.JobStateRunning
	}
	if status.FailedJobs > 0 {
		return types.JobStateFailed
	}
	if status.QueuedJobs > 0 {
		return types.JobStateQueued
	}
	if status.CancelledJobs == status.TotalJobs {
		return types.JobStateCancelled
	}
	if status.SucceededJobs == status.TotalJobs {
		return types.JobStateSucceeded
	}
	return types.JobStateQueued
}

func applyReducerMetrics(status *types.RootJobStatus, result *types.Result) {
	if status == nil || result == nil || result.Payload == nil {
		return
	}
	raw, ok := result.Payload["aggregate_metrics"].(map[string]any)
	if !ok {
		return
	}
	status.ChildrenTotal = intFromAny(raw["children_total"])
	status.ChildrenSucceeded = intFromAny(raw["children_succeeded"])
	status.ChildrenFailed = intFromAny(raw["children_failed"])
	status.CoverageFraction = floatFromAny(raw["coverage_fraction"])
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
