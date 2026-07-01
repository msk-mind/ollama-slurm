package types

import "time"

type JobState string

const (
	JobStateAccepted    JobState = "accepted"
	JobStateQueued      JobState = "queued"
	JobStateRunning     JobState = "running"
	JobStateSucceeded   JobState = "succeeded"
	JobStateFailed      JobState = "failed"
	JobStateCancelled   JobState = "cancelled"
	JobStatePreempted   JobState = "preempted"
	JobStateTimedOut    JobState = "timed_out"
	JobStateDispatching JobState = "dispatching"
)

type InputRef struct {
	Type           string         `json:"type"`
	URI            string         `json:"uri"`
	ContentHash    string         `json:"content_hash,omitempty"`
	Classification string         `json:"classification,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type Constraints struct {
	MaxInputTokens            int    `json:"max_input_tokens,omitempty"`
	MaxOutputTokens           int    `json:"max_output_tokens,omitempty"`
	RetrievedChunkBudget      int    `json:"retrieved_chunk_budget,omitempty"`
	PerChunkCompressionBudget int    `json:"per_chunk_compression_budget,omitempty"`
	FinalEvidencePackBudget   int    `json:"final_evidence_pack_budget,omitempty"`
	RemoteModelContextBudget  int    `json:"remote_model_context_budget,omitempty"`
	MaxRuntimeSeconds         int    `json:"max_runtime_seconds,omitempty"`
	Priority                  string `json:"priority,omitempty"`
	Confidentiality           string `json:"confidentiality,omitempty"`
	AllowRemoteEscalation     bool   `json:"allow_remote_escalation,omitempty"`
}

type ExecutionProfile struct {
	Backend        string `json:"backend,omitempty"`
	Tier           string `json:"tier,omitempty"`
	Model          string `json:"model,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	Accelerator    string `json:"accelerator,omitempty"`
	QOS            string `json:"qos,omitempty"`
	NodeList       string `json:"nodelist,omitempty"`
	Constraint     string `json:"constraint,omitempty"`
	ContainerImage string `json:"container_image,omitempty"`
}

type OrchestrationRequest struct {
	ParentJobID     string   `json:"parent_job_id,omitempty"`
	RootJobID       string   `json:"root_job_id,omitempty"`
	Strategy        string   `json:"strategy,omitempty"`
	ShardKey        string   `json:"shard_key,omitempty"`
	ShardIndex      int      `json:"shard_index,omitempty"`
	ShardCount      int      `json:"shard_count,omitempty"`
	AggregationKey  string   `json:"aggregation_key,omitempty"`
	DependsOnJobIDs []string `json:"depends_on_job_ids,omitempty"`
}

type OrchestrationInfo struct {
	ParentJobID     string   `json:"parent_job_id,omitempty"`
	RootJobID       string   `json:"root_job_id,omitempty"`
	Strategy        string   `json:"strategy,omitempty"`
	ShardKey        string   `json:"shard_key,omitempty"`
	ShardIndex      int      `json:"shard_index,omitempty"`
	ShardCount      int      `json:"shard_count,omitempty"`
	AggregationKey  string   `json:"aggregation_key,omitempty"`
	DependsOnJobIDs []string `json:"depends_on_job_ids,omitempty"`
	ChildJobIDs     []string `json:"child_job_ids,omitempty"`
}

type OutputSchemaRef struct {
	Name string `json:"name"`
}

type SubmitJobRequest struct {
	TaskType         string               `json:"task_type"`
	InputRefs        []InputRef           `json:"input_refs"`
	TaskParams       map[string]any       `json:"task_params,omitempty"`
	Constraints      Constraints          `json:"constraints,omitempty"`
	ExecutionProfile ExecutionProfile     `json:"execution_profile,omitempty"`
	Orchestration    OrchestrationRequest `json:"orchestration,omitempty"`
	OutputSchema     OutputSchemaRef      `json:"output_schema"`
	IdempotencyKey   string               `json:"idempotency_key,omitempty"`
}

type Result struct {
	SchemaName    string         `json:"schema_name"`
	SchemaVersion string         `json:"schema_version"`
	Payload       map[string]any `json:"payload"`
}

type Artifact struct {
	ArtifactID     string `json:"artifact_id,omitempty"`
	ArtifactType   string `json:"artifact_type,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	Path           string `json:"path,omitempty"`
	Classification string `json:"classification,omitempty"`
}

type ProgressInfo struct {
	State       string         `json:"state,omitempty"`
	Phase       string         `json:"phase,omitempty"`
	Percent     int            `json:"percent,omitempty"`
	Message     string         `json:"message,omitempty"`
	Timestamp   *time.Time     `json:"timestamp,omitempty"`
	LastUpdated *time.Time     `json:"last_updated,omitempty"`
	Metrics     map[string]any `json:"metrics,omitempty"`
}

type Job struct {
	ID                     string             `json:"job_id"`
	TaskType               string             `json:"task_type"`
	State                  JobState           `json:"state"`
	SubmittedBy            string             `json:"submitted_by,omitempty"`
	Request                SubmitJobRequest   `json:"request"`
	Result                 *Result            `json:"result,omitempty"`
	RuntimeDiagnostics     map[string]any     `json:"runtime_diagnostics,omitempty"`
	ExecutionQuality       string             `json:"execution_quality,omitempty"`
	DegradedLocalExecution bool               `json:"degraded_local_execution"`
	RetryRecommended       bool               `json:"retry_recommended"`
	Artifacts              []Artifact         `json:"artifacts,omitempty"`
	Progress               *ProgressInfo      `json:"progress,omitempty"`
	CreatedAt              time.Time          `json:"created_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
	SubmittedAt            time.Time          `json:"submitted_at"`
	StartedAt              *time.Time         `json:"started_at,omitempty"`
	CompletedAt            *time.Time         `json:"completed_at,omitempty"`
	ParentJobID            string             `json:"parent_job_id,omitempty"`
	RootJobID              string             `json:"root_job_id,omitempty"`
	Orchestration          *OrchestrationInfo `json:"orchestration,omitempty"`
	BackendKind            string             `json:"backend_kind,omitempty"`
	BackendRunID           string             `json:"backend_run_id,omitempty"`
	BackendState           string             `json:"backend_state,omitempty"`
	BackendExitCode        string             `json:"backend_exit_code,omitempty"`
	ResultError            string             `json:"result_error,omitempty"`
	CacheKey               string             `json:"cache_key,omitempty"`
	CacheStatus            string             `json:"cache_status,omitempty"`
}

type SubmitJobResponse struct {
	JobID     string      `json:"job_id"`
	State     JobState    `json:"state"`
	Cache     CacheStatus `json:"cache"`
	StatusURL string      `json:"status_url"`
}

type ParallelChildRequest struct {
	InputRefs       []InputRef     `json:"input_refs"`
	TaskParams      map[string]any `json:"task_params,omitempty"`
	ShardKey        string         `json:"shard_key,omitempty"`
	ShardIndex      int            `json:"shard_index,omitempty"`
	ShardCount      int            `json:"shard_count,omitempty"`
	AggregationKey  string         `json:"aggregation_key,omitempty"`
	DependsOnJobIDs []string       `json:"depends_on_job_ids,omitempty"`
}

type ParallelReducerRequest struct {
	TaskType         string           `json:"task_type,omitempty"`
	TaskParams       map[string]any   `json:"task_params,omitempty"`
	InputRefs        []InputRef       `json:"input_refs,omitempty"`
	OutputSchema     OutputSchemaRef  `json:"output_schema,omitempty"`
	ExecutionProfile ExecutionProfile `json:"execution_profile,omitempty"`
	Constraints      Constraints      `json:"constraints,omitempty"`
	AggregationKey   string           `json:"aggregation_key,omitempty"`
}

type SubmitParallelJobsRequest struct {
	TaskType         string                  `json:"task_type"`
	TaskParams       map[string]any          `json:"task_params,omitempty"`
	Constraints      Constraints             `json:"constraints,omitempty"`
	ExecutionProfile ExecutionProfile        `json:"execution_profile,omitempty"`
	OutputSchema     OutputSchemaRef         `json:"output_schema"`
	RootJobID        string                  `json:"root_job_id,omitempty"`
	ParentJobID      string                  `json:"parent_job_id,omitempty"`
	Strategy         string                  `json:"strategy,omitempty"`
	Children         []ParallelChildRequest  `json:"children"`
	Reducer          *ParallelReducerRequest `json:"reducer,omitempty"`
}

type ParallelChildSubmission struct {
	JobID          string      `json:"job_id"`
	State          JobState    `json:"state"`
	Cache          CacheStatus `json:"cache"`
	StatusURL      string      `json:"status_url"`
	ShardKey       string      `json:"shard_key,omitempty"`
	ShardIndex     int         `json:"shard_index,omitempty"`
	ShardCount     int         `json:"shard_count,omitempty"`
	AggregationKey string      `json:"aggregation_key,omitempty"`
}

type SubmitParallelJobsResponse struct {
	RootJobID   string                    `json:"root_job_id"`
	ParentJobID string                    `json:"parent_job_id,omitempty"`
	Strategy    string                    `json:"strategy"`
	ChildCount  int                       `json:"child_count"`
	Children    []ParallelChildSubmission `json:"children"`
	ReducerJob  *SubmitJobResponse        `json:"reducer_job,omitempty"`
}

type RetryFailedRootShardsRequest struct {
	RootJobID        string `json:"root_job_id"`
	IncludeCancelled bool   `json:"include_cancelled,omitempty"`
	ResubmitReducer  bool   `json:"resubmit_reducer,omitempty"`
}

type RetriedShardSubmission struct {
	PreviousJobID  string      `json:"previous_job_id"`
	JobID          string      `json:"job_id"`
	State          JobState    `json:"state"`
	Cache          CacheStatus `json:"cache"`
	StatusURL      string      `json:"status_url"`
	ShardKey       string      `json:"shard_key,omitempty"`
	ShardIndex     int         `json:"shard_index,omitempty"`
	ShardCount     int         `json:"shard_count,omitempty"`
	AggregationKey string      `json:"aggregation_key,omitempty"`
}

type SkippedShardRetry struct {
	JobID      string `json:"job_id"`
	ShardKey   string `json:"shard_key,omitempty"`
	ShardIndex int    `json:"shard_index,omitempty"`
	ShardCount int    `json:"shard_count,omitempty"`
	Reason     string `json:"reason"`
}

type RetryFailedRootShardsResponse struct {
	RootJobID                   string                   `json:"root_job_id"`
	RetriedCount                int                      `json:"retried_count"`
	RetriedShards               []RetriedShardSubmission `json:"retried_shards,omitempty"`
	SkippedCount                int                      `json:"skipped_count"`
	SkippedShards               []SkippedShardRetry      `json:"skipped_shards,omitempty"`
	ReducerJob                  *SubmitJobResponse       `json:"reducer_job,omitempty"`
	CumulativeRetriedShards     int                      `json:"cumulative_retried_shards,omitempty"`
	RemainingRetriedShardBudget int                      `json:"remaining_retried_shard_budget,omitempty"`
}

type ReleaseDeferredRootChunksRequest struct {
	RootJobID            string `json:"root_job_id"`
	MaxAdditionalBatches int    `json:"max_additional_batches,omitempty"`
}

type ReleaseDeferredRootChunksResponse struct {
	RootJobID                     string        `json:"root_job_id"`
	ReleasedChunks                int           `json:"released_chunks"`
	ReleasedChildren              int           `json:"released_children"`
	ReducerReleased               bool          `json:"reducer_released,omitempty"`
	CumulativeForcedReleaseChunks int           `json:"cumulative_forced_release_chunks,omitempty"`
	RemainingForcedReleaseBudget  int           `json:"remaining_forced_release_budget,omitempty"`
	RootStatus                    RootJobStatus `json:"root_status"`
}

type CacheStatus struct {
	Status string `json:"status"`
}

type ArtifactMetadata struct {
	ArtifactID     string `json:"artifact_id"`
	ArtifactType   string `json:"artifact_type"`
	Classification string `json:"classification,omitempty"`
	ContentHash    string `json:"content_hash,omitempty"`
	SourceJobID    string `json:"source_job_id"`
	SourceTaskType string `json:"source_task_type,omitempty"`
	SourceSchema   string `json:"source_schema,omitempty"`
	SubmittedBy    string `json:"submitted_by,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
}

type CacheLookupResponse struct {
	Status        string `json:"status"`
	CacheKey      string `json:"cache_key,omitempty"`
	TaskType      string `json:"task_type,omitempty"`
	SchemaName    string `json:"schema_name,omitempty"`
	SourceJobID   string `json:"source_job_id,omitempty"`
	ArtifactCount int    `json:"artifact_count,omitempty"`
}

type CancelJobResponse struct {
	JobID string   `json:"job_id"`
	State JobState `json:"state"`
}

type JobLogs struct {
	JobID      string   `json:"job_id"`
	State      JobState `json:"state"`
	Stream     string   `json:"stream"`
	Content    string   `json:"content"`
	Truncated  bool     `json:"truncated"`
	MaxBytes   int      `json:"max_bytes,omitempty"`
	SourceRefs []string `json:"source_refs,omitempty"`
}

type JobResultRelease struct {
	JobID                  string         `json:"job_id"`
	State                  JobState       `json:"state"`
	Result                 *Result        `json:"result,omitempty"`
	RuntimeDiagnostics     map[string]any `json:"runtime_diagnostics,omitempty"`
	ExecutionQuality       string         `json:"execution_quality,omitempty"`
	DegradedLocalExecution bool           `json:"degraded_local_execution"`
	RetryRecommended       bool           `json:"retry_recommended"`
	Artifacts              []Artifact     `json:"artifacts,omitempty"`
}

type JobRetryRecommendation struct {
	JobID             string           `json:"job_id"`
	Recommended       bool             `json:"recommended"`
	Reason            string           `json:"reason,omitempty"`
	TaskType          string           `json:"task_type,omitempty"`
	ExecutionProfile  ExecutionProfile `json:"execution_profile,omitempty"`
	PlacementHint     PlacementHint    `json:"placement_hint,omitempty"`
	SourceResultError string           `json:"source_result_error,omitempty"`
}

type PlacementHint struct {
	BackendPreference string `json:"backend_preference,omitempty"`
	TierPreference    string `json:"tier_preference,omitempty"`
	QOS               string `json:"qos,omitempty"`
	NodeList          string `json:"nodelist,omitempty"`
	Constraint        string `json:"constraint,omitempty"`
	Preemptible       bool   `json:"preemptible,omitempty"`
	Rationale         string `json:"rationale,omitempty"`
}

type RootJobStatus struct {
	RootJobID            string        `json:"root_job_id"`
	State                JobState      `json:"state"`
	TotalJobs            int           `json:"total_jobs"`
	QueuedJobs           int           `json:"queued_jobs"`
	RunningJobs          int           `json:"running_jobs"`
	SucceededJobs        int           `json:"succeeded_jobs"`
	FailedJobs           int           `json:"failed_jobs"`
	CancelledJobs        int           `json:"cancelled_jobs"`
	ReducerJobID         string        `json:"reducer_job_id,omitempty"`
	ReducerState         JobState      `json:"reducer_state,omitempty"`
	ChildJobIDs          []string      `json:"child_job_ids,omitempty"`
	Progress             *ProgressInfo `json:"progress,omitempty"`
	RepresentativeError  string        `json:"representative_error,omitempty"`
	AggregationKeys      []string      `json:"aggregation_keys,omitempty"`
	ChildrenTotal        int           `json:"children_total,omitempty"`
	ChildrenSucceeded    int           `json:"children_succeeded,omitempty"`
	ChildrenFailed       int           `json:"children_failed,omitempty"`
	CoverageFraction     float64       `json:"coverage_fraction,omitempty"`
	DispatchingChildren  int           `json:"dispatching_children,omitempty"`
	PendingChildren      int           `json:"pending_children,omitempty"`
	ActiveChunks         int           `json:"active_chunks,omitempty"`
	PendingChunks        int           `json:"pending_chunks,omitempty"`
	ReducerDeferred      bool          `json:"reducer_deferred,omitempty"`
	ForcedReleasedChunks int           `json:"forced_released_chunks,omitempty"`
	RetriedShardActions  int           `json:"retried_shard_actions,omitempty"`
}
