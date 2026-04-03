package domain

import "time"

type CredentialType string

const (
	CredentialTypeSSHKey     CredentialType = "ssh_key"
	CredentialTypeHTTPSToken CredentialType = "https_token"
	CredentialTypeAPIToken   CredentialType = "api_token"
)

type ProviderType string

const (
	ProviderGitHub ProviderType = "github"
	ProviderGogs   ProviderType = "gogs"
)

type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

type TaskType string

const (
	TaskTypeGitMirror TaskType = "git_mirror"
	TaskTypeSVNImport TaskType = "svn_import"
)

type SubmoduleRewriteProtocol string

const (
	SubmoduleRewriteProtocolInherit SubmoduleRewriteProtocol = "inherit"
	SubmoduleRewriteProtocolHTTP    SubmoduleRewriteProtocol = "http"
	SubmoduleRewriteProtocolSSH     SubmoduleRewriteProtocol = "ssh"
)

type ExecutionStatus string

const (
	ExecutionStatusRunning ExecutionStatus = "running"
	ExecutionStatusSuccess ExecutionStatus = "success"
	ExecutionStatusFailed  ExecutionStatus = "failed"
)

type TriggerType string

const (
	TriggerManual   TriggerType = "manual"
	TriggerSchedule TriggerType = "schedule"
	TriggerWebhook  TriggerType = "webhook"
)

type TriggerConfig struct {
	Cron            string `json:"cron"`
	WebhookSecret   string `json:"webhookSecret"`
	EnableSchedule  bool   `json:"enableSchedule"`
	EnableWebhook   bool   `json:"enableWebhook"`
	BranchReference string `json:"branchReference"`
}

type ProviderConfig struct {
	Provider            ProviderType `json:"provider"`
	Namespace           string       `json:"namespace"`
	Visibility          Visibility   `json:"visibility"`
	DescriptionTemplate string       `json:"descriptionTemplate"`
	BaseAPIURL          string       `json:"baseApiUrl"`
}

type SVNConfig struct {
	TrunkPath       string `json:"trunkPath"`
	BranchesPath    string `json:"branchesPath"`
	TagsPath        string `json:"tagsPath"`
	StartRevision   string `json:"startRevision"`
	AuthorsFilePath string `json:"authorsFilePath"`
	AuthorDomain    string `json:"authorDomain"`
}

type SyncTask struct {
	ID                             int64                    `json:"id"`
	TaskType                       TaskType                 `json:"taskType"`
	Name                           string                   `json:"name"`
	SourceRepoURL                  string                   `json:"sourceRepoUrl"`
	TargetRepoURL                  string                   `json:"targetRepoUrl"`
	CacheBasePath                  string                   `json:"cacheBasePath"`
	SourceCredentialID             *int64                   `json:"sourceCredentialId"`
	SubmoduleSourceCredentialID    *int64                   `json:"submoduleSourceCredentialId"`
	TargetCredentialID             *int64                   `json:"targetCredentialId"`
	SubmoduleTargetCredentialID    *int64                   `json:"submoduleTargetCredentialId"`
	TargetAPICredentialID          *int64                   `json:"targetApiCredentialId"`
	SubmoduleTargetAPICredentialID *int64                   `json:"submoduleTargetApiCredentialId"`
	SubmoduleRewriteProtocol       SubmoduleRewriteProtocol `json:"submoduleRewriteProtocol"`
	Enabled                        bool                     `json:"enabled"`
	RecursiveSubmodules            bool                     `json:"recursiveSubmodules"`
	SyncAllRefs                    bool                     `json:"syncAllRefs"`
	TriggerConfig                  TriggerConfig            `json:"triggerConfig"`
	ProviderConfig                 ProviderConfig           `json:"providerConfig"`
	SVNConfig                      SVNConfig                `json:"svnConfig"`
	ScheduleCron                   string                   `json:"scheduleCron,omitempty"`
	NextRunAt                      *time.Time               `json:"nextRunAt,omitempty"`
	LastExecutionID                *int64                   `json:"lastExecutionId,omitempty"`
	LastExecutionStatus            string                   `json:"lastExecutionStatus,omitempty"`
	LastExecutionAt                *time.Time               `json:"lastExecutionAt,omitempty"`
	LastExecutionRepoCount         int                      `json:"lastExecutionRepoCount,omitempty"`
	LastCreatedRepoCount           int                      `json:"lastCreatedRepoCount,omitempty"`
	CreatedAt                      time.Time                `json:"createdAt"`
	UpdatedAt                      time.Time                `json:"updatedAt"`
}

type Credential struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Type         CredentialType `json:"type"`
	Username     string         `json:"username,omitempty"`
	Secret       string         `json:"secret,omitempty"`
	SecretMasked string         `json:"secretMasked,omitempty"`
	Scope        string         `json:"scope,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type SyncExecution struct {
	ID               int64           `json:"id"`
	TaskID           int64           `json:"taskId"`
	TriggerType      TriggerType     `json:"triggerType"`
	Status           ExecutionStatus `json:"status"`
	StartedAt        time.Time       `json:"startedAt"`
	FinishedAt       *time.Time      `json:"finishedAt,omitempty"`
	RepoCount        int             `json:"repoCount"`
	CreatedRepoCount int             `json:"createdRepoCount"`
	FailedNodeCount  int             `json:"failedNodeCount"`
	SummaryLog       string          `json:"summaryLog"`
	LogCount         int             `json:"logCount"`
	LastLogID        int64           `json:"lastLogId"`
}

type ExecutionLogEntry struct {
	ID          int64     `json:"id"`
	ExecutionID int64     `json:"executionId"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SyncExecutionNode struct {
	ID              int64           `json:"id"`
	ExecutionID     int64           `json:"executionId"`
	ParentNodeID    *int64          `json:"parentNodeId,omitempty"`
	RepoPath        string          `json:"repoPath"`
	SourceRepoURL   string          `json:"sourceRepoUrl"`
	TargetRepoURL   string          `json:"targetRepoUrl"`
	ReferenceCommit string          `json:"referenceCommit"`
	Depth           int             `json:"depth"`
	CacheKey        string          `json:"cacheKey"`
	CacheHit        bool            `json:"cacheHit"`
	AutoCreated     bool            `json:"autoCreated"`
	CreateDuration  int64           `json:"createDurationMs"`
	FetchDuration   int64           `json:"fetchDurationMs"`
	PushDuration    int64           `json:"pushDurationMs"`
	Status          ExecutionStatus `json:"status"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
}

type RepoCache struct {
	ID               int64      `json:"id"`
	CacheKey         string     `json:"cacheKey"`
	SourceRepoURL    string     `json:"sourceRepoUrl"`
	AuthContext      string     `json:"authContext"`
	CachePath        string     `json:"cachePath"`
	LinkedTaskCount  int        `json:"linkedTaskCount"`
	LastFetchAt      *time.Time `json:"lastFetchAt,omitempty"`
	LastUsedAt       *time.Time `json:"lastUsedAt,omitempty"`
	HitCount         int        `json:"hitCount"`
	SizeBytes        int64      `json:"sizeBytes"`
	HealthStatus     string     `json:"healthStatus"`
	LastErrorMessage string     `json:"lastErrorMessage,omitempty"`
}

type CacheMoveRequest struct {
	CachePath string `json:"cachePath"`
}

type ExecutionDetail struct {
	Execution SyncExecution       `json:"execution"`
	Task      SyncTask            `json:"task"`
	Nodes     []SyncExecutionNode `json:"nodes"`
}

type WebhookEvent struct {
	ID          int64     `json:"id"`
	TaskID      int64     `json:"taskId"`
	Provider    string    `json:"provider"`
	EventType   string    `json:"eventType"`
	Ref         string    `json:"ref"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	ExecutionID *int64    `json:"executionId,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}
