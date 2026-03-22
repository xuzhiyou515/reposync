package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"reposync/backend/internal/domain"
	"reposync/backend/internal/security"
)

type Store struct {
	db  *sql.DB
	box *security.SecretBox
}

type scanner interface {
	Scan(dest ...any) error
}

func New(path string, box *security.SecretBox) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db, box: box}
	if err := store.init(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sync_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  source_repo_url TEXT NOT NULL,
  target_repo_url TEXT NOT NULL,
  source_credential_id INTEGER,
  target_credential_id INTEGER,
  enabled INTEGER NOT NULL,
  recursive_submodules INTEGER NOT NULL,
  sync_all_refs INTEGER NOT NULL,
  trigger_config TEXT NOT NULL,
  provider_config TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS credentials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  username TEXT,
  secret_encrypted TEXT NOT NULL,
  scope TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sync_executions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  trigger_type TEXT NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  repo_count INTEGER NOT NULL DEFAULT 0,
  created_repo_count INTEGER NOT NULL DEFAULT 0,
  failed_node_count INTEGER NOT NULL DEFAULT 0,
  summary_log TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sync_execution_nodes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  execution_id INTEGER NOT NULL,
  parent_node_id INTEGER,
  repo_path TEXT NOT NULL,
  source_repo_url TEXT NOT NULL,
  target_repo_url TEXT NOT NULL,
  reference_commit TEXT NOT NULL DEFAULT '',
  depth INTEGER NOT NULL,
  cache_key TEXT NOT NULL DEFAULT '',
  cache_hit INTEGER NOT NULL DEFAULT 0,
  auto_created INTEGER NOT NULL DEFAULT 0,
  create_duration_ms INTEGER NOT NULL DEFAULT 0,
  fetch_duration_ms INTEGER NOT NULL DEFAULT 0,
  push_duration_ms INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  error_message TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS repo_caches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  cache_key TEXT NOT NULL UNIQUE,
  source_repo_url TEXT NOT NULL,
  auth_context TEXT NOT NULL,
  cache_path TEXT NOT NULL,
  last_fetch_at TEXT,
  last_used_at TEXT,
  hit_count INTEGER NOT NULL DEFAULT 0,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  health_status TEXT NOT NULL DEFAULT 'ready',
  last_error_message TEXT NOT NULL DEFAULT ''
);`)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func timeString(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, raw)
	return parsed
}

func parseNullableTime(raw sql.NullString) *time.Time {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	parsed := parseTime(raw.String)
	return &parsed
}

func toJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func scanTask(row scanner, withLatest bool) (domain.SyncTask, error) {
	var task domain.SyncTask
	var srcCredential sql.NullInt64
	var targetCredential sql.NullInt64
	var enabled, recursive, syncAll int
	var triggerJSON, providerJSON string
	var createdAt, updatedAt string
	var lastStatus string
	var lastStarted sql.NullString
	var lastRepoCount, lastCreatedCount int
	var err error

	if withLatest {
		err = row.Scan(
			&task.ID, &task.Name, &task.SourceRepoURL, &task.TargetRepoURL,
			&srcCredential, &targetCredential,
			&enabled, &recursive, &syncAll,
			&triggerJSON, &providerJSON,
			&createdAt, &updatedAt,
			&lastStatus, &lastStarted, &lastRepoCount, &lastCreatedCount,
		)
	} else {
		err = row.Scan(
			&task.ID, &task.Name, &task.SourceRepoURL, &task.TargetRepoURL,
			&srcCredential, &targetCredential,
			&enabled, &recursive, &syncAll,
			&triggerJSON, &providerJSON,
			&createdAt, &updatedAt,
		)
	}
	if err != nil {
		return task, err
	}

	if srcCredential.Valid {
		task.SourceCredentialID = &srcCredential.Int64
	}
	if targetCredential.Valid {
		task.TargetCredentialID = &targetCredential.Int64
	}
	task.Enabled = enabled == 1
	task.RecursiveSubmodules = recursive == 1
	task.SyncAllRefs = syncAll == 1
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	_ = json.Unmarshal([]byte(triggerJSON), &task.TriggerConfig)
	_ = json.Unmarshal([]byte(providerJSON), &task.ProviderConfig)
	if withLatest {
		task.LastExecutionStatus = lastStatus
		task.LastExecutionAt = parseNullableTime(lastStarted)
		task.LastExecutionRepoCount = lastRepoCount
		task.LastCreatedRepoCount = lastCreatedCount
	}
	return task, nil
}

func (s *Store) SaveTask(ctx context.Context, task domain.SyncTask) (domain.SyncTask, error) {
	now := time.Now().UTC()
	if !task.SyncAllRefs {
		task.SyncAllRefs = true
	}
	if task.ProviderConfig.Provider == "" {
		task.ProviderConfig.Provider = domain.ProviderGitHub
	}
	if task.ProviderConfig.Visibility == "" {
		task.ProviderConfig.Visibility = domain.VisibilityPrivate
	}

	if task.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
INSERT INTO sync_tasks (
  name, source_repo_url, target_repo_url, source_credential_id, target_credential_id,
  enabled, recursive_submodules, sync_all_refs, trigger_config, provider_config, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			task.Name, task.SourceRepoURL, task.TargetRepoURL, task.SourceCredentialID, task.TargetCredentialID,
			boolInt(task.Enabled), boolInt(task.RecursiveSubmodules), boolInt(task.SyncAllRefs),
			toJSON(task.TriggerConfig), toJSON(task.ProviderConfig), timeString(now), timeString(now),
		)
		if err != nil {
			return domain.SyncTask{}, err
		}
		task.ID, _ = res.LastInsertId()
	} else {
		_, err := s.db.ExecContext(ctx, `
UPDATE sync_tasks SET
  name = ?, source_repo_url = ?, target_repo_url = ?, source_credential_id = ?, target_credential_id = ?,
  enabled = ?, recursive_submodules = ?, sync_all_refs = ?, trigger_config = ?, provider_config = ?, updated_at = ?
WHERE id = ?`,
			task.Name, task.SourceRepoURL, task.TargetRepoURL, task.SourceCredentialID, task.TargetCredentialID,
			boolInt(task.Enabled), boolInt(task.RecursiveSubmodules), boolInt(task.SyncAllRefs),
			toJSON(task.TriggerConfig), toJSON(task.ProviderConfig), timeString(now), task.ID,
		)
		if err != nil {
			return domain.SyncTask{}, err
		}
	}
	return s.GetTask(ctx, task.ID)
}

func (s *Store) GetTask(ctx context.Context, id int64) (domain.SyncTask, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, source_repo_url, target_repo_url, source_credential_id, target_credential_id,
enabled, recursive_submodules, sync_all_refs, trigger_config, provider_config, created_at, updated_at
FROM sync_tasks WHERE id = ?`, id)
	return scanTask(row, false)
}

func (s *Store) ListTasks(ctx context.Context) ([]domain.SyncTask, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  t.id, t.name, t.source_repo_url, t.target_repo_url, t.source_credential_id, t.target_credential_id,
  t.enabled, t.recursive_submodules, t.sync_all_refs, t.trigger_config, t.provider_config, t.created_at, t.updated_at,
  COALESCE(e.status, ''), e.started_at, COALESCE(e.repo_count, 0), COALESCE(e.created_repo_count, 0)
FROM sync_tasks t
LEFT JOIN sync_executions e ON e.id = (
  SELECT se.id FROM sync_executions se WHERE se.task_id = t.id ORDER BY se.started_at DESC LIMIT 1
)
ORDER BY t.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []domain.SyncTask
	for rows.Next() {
		task, scanErr := scanTask(rows, true)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) DeleteTask(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sync_tasks WHERE id = ?`, id)
	return err
}

func maskSecret(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:2] + "****" + value[len(value)-2:]
}

func (s *Store) SaveCredential(ctx context.Context, credential domain.Credential) (domain.Credential, error) {
	now := time.Now().UTC()
	encrypted, err := s.box.Encrypt(credential.Secret)
	if err != nil {
		return domain.Credential{}, err
	}
	if credential.ID == 0 {
		res, err := s.db.ExecContext(ctx, `
INSERT INTO credentials (name, type, username, secret_encrypted, scope, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			credential.Name, credential.Type, credential.Username, encrypted, credential.Scope, timeString(now), timeString(now),
		)
		if err != nil {
			return domain.Credential{}, err
		}
		credential.ID, _ = res.LastInsertId()
	} else {
		_, err := s.db.ExecContext(ctx, `
UPDATE credentials SET name = ?, type = ?, username = ?, secret_encrypted = ?, scope = ?, updated_at = ?
WHERE id = ?`,
			credential.Name, credential.Type, credential.Username, encrypted, credential.Scope, timeString(now), credential.ID,
		)
		if err != nil {
			return domain.Credential{}, err
		}
	}
	return s.GetCredential(ctx, credential.ID)
}

func (s *Store) scanCredential(row scanner) (domain.Credential, error) {
	var credential domain.Credential
	var encrypted string
	var createdAt, updatedAt string
	if err := row.Scan(&credential.ID, &credential.Name, &credential.Type, &credential.Username, &encrypted, &credential.Scope, &createdAt, &updatedAt); err != nil {
		return credential, err
	}
	secret, err := s.box.Decrypt(encrypted)
	if err != nil {
		return credential, err
	}
	credential.Secret = secret
	credential.SecretMasked = maskSecret(secret)
	credential.CreatedAt = parseTime(createdAt)
	credential.UpdatedAt = parseTime(updatedAt)
	return credential, nil
}

func (s *Store) GetCredential(ctx context.Context, id int64) (domain.Credential, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, type, username, secret_encrypted, scope, created_at, updated_at
FROM credentials WHERE id = ?`, id)
	return s.scanCredential(row)
}

func (s *Store) ListCredentials(ctx context.Context) ([]domain.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, type, username, secret_encrypted, scope, created_at, updated_at
FROM credentials ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var credentials []domain.Credential
	for rows.Next() {
		credential, scanErr := s.scanCredential(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func (s *Store) DeleteCredential(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, id)
	return err
}

func (s *Store) CredentialByOptionalID(ctx context.Context, id *int64) (*domain.Credential, error) {
	if id == nil {
		return nil, nil
	}
	credential, err := s.GetCredential(ctx, *id)
	if err != nil {
		return nil, err
	}
	return &credential, nil
}

func (s *Store) CreateExecution(ctx context.Context, execution domain.SyncExecution) (domain.SyncExecution, error) {
	startedAt := time.Now().UTC()
	execution.StartedAt = startedAt
	res, err := s.db.ExecContext(ctx, `
INSERT INTO sync_executions (task_id, trigger_type, status, started_at, repo_count, created_repo_count, failed_node_count, summary_log)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		execution.TaskID, execution.TriggerType, execution.Status, timeString(startedAt),
		execution.RepoCount, execution.CreatedRepoCount, execution.FailedNodeCount, execution.SummaryLog,
	)
	if err != nil {
		return domain.SyncExecution{}, err
	}
	execution.ID, _ = res.LastInsertId()
	return execution, nil
}

func (s *Store) UpdateExecution(ctx context.Context, execution domain.SyncExecution) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sync_executions
SET status = ?, finished_at = ?, repo_count = ?, created_repo_count = ?, failed_node_count = ?, summary_log = ?
WHERE id = ?`,
		execution.Status, nullableTime(execution.FinishedAt), execution.RepoCount, execution.CreatedRepoCount,
		execution.FailedNodeCount, execution.SummaryLog, execution.ID,
	)
	return err
}

func (s *Store) CreateExecutionNode(ctx context.Context, node domain.SyncExecutionNode) (domain.SyncExecutionNode, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO sync_execution_nodes (
 execution_id, parent_node_id, repo_path, source_repo_url, target_repo_url, reference_commit,
 depth, cache_key, cache_hit, auto_created, create_duration_ms, fetch_duration_ms, push_duration_ms, status, error_message
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.ExecutionID, node.ParentNodeID, node.RepoPath, node.SourceRepoURL, node.TargetRepoURL, node.ReferenceCommit,
		node.Depth, node.CacheKey, boolInt(node.CacheHit), boolInt(node.AutoCreated), node.CreateDuration,
		node.FetchDuration, node.PushDuration, node.Status, node.ErrorMessage,
	)
	if err != nil {
		return domain.SyncExecutionNode{}, err
	}
	node.ID, _ = res.LastInsertId()
	return node, nil
}

func (s *Store) UpdateExecutionNode(ctx context.Context, node domain.SyncExecutionNode) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sync_execution_nodes SET
 parent_node_id = ?, repo_path = ?, source_repo_url = ?, target_repo_url = ?, reference_commit = ?,
 depth = ?, cache_key = ?, cache_hit = ?, auto_created = ?, create_duration_ms = ?, fetch_duration_ms = ?,
 push_duration_ms = ?, status = ?, error_message = ?
WHERE id = ?`,
		node.ParentNodeID, node.RepoPath, node.SourceRepoURL, node.TargetRepoURL, node.ReferenceCommit,
		node.Depth, node.CacheKey, boolInt(node.CacheHit), boolInt(node.AutoCreated), node.CreateDuration,
		node.FetchDuration, node.PushDuration, node.Status, node.ErrorMessage, node.ID,
	)
	return err
}

func (s *Store) ListExecutionsForTask(ctx context.Context, taskID int64) ([]domain.SyncExecution, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, task_id, trigger_type, status, started_at, finished_at, repo_count, created_repo_count, failed_node_count, summary_log
FROM sync_executions WHERE task_id = ? ORDER BY started_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var executions []domain.SyncExecution
	for rows.Next() {
		var item domain.SyncExecution
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.TaskID, &item.TriggerType, &item.Status, &startedAt, &finishedAt, &item.RepoCount, &item.CreatedRepoCount, &item.FailedNodeCount, &item.SummaryLog); err != nil {
			return nil, err
		}
		item.StartedAt = parseTime(startedAt)
		item.FinishedAt = parseNullableTime(finishedAt)
		executions = append(executions, item)
	}
	return executions, rows.Err()
}

func (s *Store) GetExecutionDetail(ctx context.Context, id int64) (domain.ExecutionDetail, error) {
	var detail domain.ExecutionDetail
	row := s.db.QueryRowContext(ctx, `
SELECT id, task_id, trigger_type, status, started_at, finished_at, repo_count, created_repo_count, failed_node_count, summary_log
FROM sync_executions WHERE id = ?`, id)
	var startedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&detail.Execution.ID, &detail.Execution.TaskID, &detail.Execution.TriggerType, &detail.Execution.Status, &startedAt, &finishedAt, &detail.Execution.RepoCount, &detail.Execution.CreatedRepoCount, &detail.Execution.FailedNodeCount, &detail.Execution.SummaryLog); err != nil {
		return detail, err
	}
	detail.Execution.StartedAt = parseTime(startedAt)
	detail.Execution.FinishedAt = parseNullableTime(finishedAt)
	task, err := s.GetTask(ctx, detail.Execution.TaskID)
	if err != nil {
		return detail, err
	}
	detail.Task = task

	rows, err := s.db.QueryContext(ctx, `
SELECT id, execution_id, parent_node_id, repo_path, source_repo_url, target_repo_url, reference_commit, depth, cache_key, cache_hit, auto_created, create_duration_ms, fetch_duration_ms, push_duration_ms, status, error_message
FROM sync_execution_nodes WHERE execution_id = ? ORDER BY id ASC`, id)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	for rows.Next() {
		var node domain.SyncExecutionNode
		var parent sql.NullInt64
		var cacheHit, autoCreated int
		if err := rows.Scan(&node.ID, &node.ExecutionID, &parent, &node.RepoPath, &node.SourceRepoURL, &node.TargetRepoURL, &node.ReferenceCommit, &node.Depth, &node.CacheKey, &cacheHit, &autoCreated, &node.CreateDuration, &node.FetchDuration, &node.PushDuration, &node.Status, &node.ErrorMessage); err != nil {
			return detail, err
		}
		if parent.Valid {
			node.ParentNodeID = &parent.Int64
		}
		node.CacheHit = cacheHit == 1
		node.AutoCreated = autoCreated == 1
		detail.Nodes = append(detail.Nodes, node)
	}
	return detail, rows.Err()
}

func (s *Store) UpsertCache(ctx context.Context, cache domain.RepoCache) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO repo_caches (cache_key, source_repo_url, auth_context, cache_path, last_fetch_at, last_used_at, hit_count, size_bytes, health_status, last_error_message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(cache_key) DO UPDATE SET
 source_repo_url = excluded.source_repo_url,
 auth_context = excluded.auth_context,
 cache_path = excluded.cache_path,
 last_fetch_at = excluded.last_fetch_at,
 last_used_at = excluded.last_used_at,
 hit_count = excluded.hit_count,
 size_bytes = excluded.size_bytes,
 health_status = excluded.health_status,
 last_error_message = excluded.last_error_message`,
		cache.CacheKey, cache.SourceRepoURL, cache.AuthContext, cache.CachePath, nullableTime(cache.LastFetchAt),
		nullableTime(cache.LastUsedAt), cache.HitCount, cache.SizeBytes, cache.HealthStatus, cache.LastErrorMessage,
	)
	return err
}

func (s *Store) GetCacheByKey(ctx context.Context, cacheKey string) (domain.RepoCache, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, cache_key, source_repo_url, auth_context, cache_path, last_fetch_at, last_used_at, hit_count, size_bytes, health_status, last_error_message
FROM repo_caches WHERE cache_key = ?`, cacheKey)
	var item domain.RepoCache
	var lastFetch sql.NullString
	var lastUsed sql.NullString
	if err := row.Scan(&item.ID, &item.CacheKey, &item.SourceRepoURL, &item.AuthContext, &item.CachePath, &lastFetch, &lastUsed, &item.HitCount, &item.SizeBytes, &item.HealthStatus, &item.LastErrorMessage); err != nil {
		return item, err
	}
	item.LastFetchAt = parseNullableTime(lastFetch)
	item.LastUsedAt = parseNullableTime(lastUsed)
	return item, nil
}

func (s *Store) ListCaches(ctx context.Context) ([]domain.RepoCache, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, cache_key, source_repo_url, auth_context, cache_path, last_fetch_at, last_used_at, hit_count, size_bytes, health_status, last_error_message
FROM repo_caches ORDER BY last_used_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var caches []domain.RepoCache
	for rows.Next() {
		var item domain.RepoCache
		var lastFetch, lastUsed sql.NullString
		if err := rows.Scan(&item.ID, &item.CacheKey, &item.SourceRepoURL, &item.AuthContext, &item.CachePath, &lastFetch, &lastUsed, &item.HitCount, &item.SizeBytes, &item.HealthStatus, &item.LastErrorMessage); err != nil {
			return nil, err
		}
		item.LastFetchAt = parseNullableTime(lastFetch)
		item.LastUsedAt = parseNullableTime(lastUsed)
		caches = append(caches, item)
	}
	return caches, rows.Err()
}

func (s *Store) DeleteCache(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM repo_caches WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errors.New("cache not found")
	}
	return nil
}
