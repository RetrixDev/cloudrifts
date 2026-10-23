package panel

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
)

type FileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified_at"`
}

type FileList struct {
	Website Website     `json:"website"`
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

type FileRead struct {
	Website Website `json:"website"`
	Path    string  `json:"path"`
	Size    int64   `json:"size"`
	Content string  `json:"content"`
}

type WriteFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type WebsiteLogs struct {
	Website Website  `json:"website"`
	Kind    string   `json:"kind"`
	Lines   []string `json:"lines"`
	Path    string   `json:"path,omitempty"`
}

type CreateBackupResponse struct {
	Backup Backup `json:"backup"`
	Job    Job    `json:"job"`
}

type RestoreBackupResponse struct {
	Backup Backup `json:"backup"`
	Job    Job    `json:"job"`
}

func (s *Service) WebsiteBackups(id string) ([]Backup, bool, error) {
	st, err := s.store.Load()
	if err != nil {
		return nil, false, err
	}
	if findWebsite(&st, id) == nil {
		return nil, false, nil
	}
	items := []Backup{}
	for _, backup := range st.Backups {
		if backup.WebsiteID == id {
			items = append(items, backup)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, true, nil
}

func (s *Service) CreateBackup(id string) (CreateBackupResponse, bool, error) {
	var out CreateBackupResponse
	found := false
	_, err := s.store.Save(func(st *state) error {
		site := findWebsite(st, id)
		if site == nil {
			return nil
		}
		found = true
		now := time.Now().UTC()
		backup := Backup{ID: "backup-" + token(6), WebsiteID: site.ID, WebsiteDomain: site.Domain, Type: "full", Status: "queued", IncludesFiles: true, IncludesDatabase: site.App != AppPHP && site.DatabaseName != "", CreatedAt: now, UpdatedAt: now}
		job := Job{ID: "job-" + token(6), Type: "website.backup.create", Title: "Create backup", TargetID: site.ID, TargetName: site.Domain, Status: JobQueued, Steps: []JobStep{{ID: "files", Title: "Archive files", Detail: "Create a compressed snapshot of the document root", Status: StepPending}, {ID: "database", Title: "Dump database", Detail: "Export the website database when present", Status: StepPending}, {ID: "manifest", Title: "Write manifest", Detail: "Record website metadata, permissions, and restore details", Status: StepPending}}, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " backup queued for " + site.Domain}, CreatedAt: now, UpdatedAt: now}
		backup.JobID = job.ID
		st.Backups = append([]Backup{backup}, st.Backups...)
		st.Jobs = append([]Job{job}, st.Jobs...)
		out = CreateBackupResponse{Backup: backup, Job: job}
		return nil
	})
	if err == nil && found {
		s.dispatchBackupJob(id, out.Backup.ID, out.Job.ID)
	}
	return out, found, err
}

func (s *Service) RestoreBackup(siteID string, backupID string) (RestoreBackupResponse, bool, error) {
	var out RestoreBackupResponse
	found := false
	_, err := s.store.Save(func(st *state) error {
		site := findWebsite(st, siteID)
		if site == nil {
			return nil
		}
		for i := range st.Backups {
			if st.Backups[i].ID != backupID || st.Backups[i].WebsiteID != siteID {
				continue
			}
			found = true
			now := time.Now().UTC()
			job := Job{ID: "job-" + token(6), Type: "website.backup.restore", Title: "Restore backup", TargetID: site.ID, TargetName: site.Domain, Status: JobQueued, Steps: []JobStep{{ID: "preflight", Title: "Validate restore point", Detail: "Check backup files and website ownership", Status: StepPending}, {ID: "files", Title: "Restore files", Detail: "Restore document root files", Status: StepPending}, {ID: "database", Title: "Restore database", Detail: "Import database dump when present", Status: StepPending}}, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " restore queued for " + site.Domain}, CreatedAt: now, UpdatedAt: now}
			st.Backups[i].LastRestoreJobID = job.ID
			st.Backups[i].UpdatedAt = now
			st.Jobs = append([]Job{job}, st.Jobs...)
			out = RestoreBackupResponse{Backup: st.Backups[i], Job: job}
			return nil
		}
		return nil
	})
	if err == nil && found {
		s.dispatchRestoreJob(siteID, backupID, out.Job.ID)
	}
	return out, found, err
}

func (s *Service) dispatchBackupJob(siteID, backupID, jobID string) {
	if os.Getenv("CLOUDRIFTS_SYNC_JOBS") == "1" {
		s.runBackupJob(siteID, backupID, jobID)
		return
	}
	go s.runBackupJob(siteID, backupID, jobID)
}

func (s *Service) dispatchRestoreJob(siteID, backupID, jobID string) {
	if os.Getenv("CLOUDRIFTS_SYNC_JOBS") == "1" {
		s.runRestoreJob(siteID, backupID, jobID)
		return
	}
	go s.runRestoreJob(siteID, backupID, jobID)
}

func (s *Service) runBackupJob(siteID, backupID, jobID string) {
	s.markGenericJobRunning(jobID)
	site, ok, err := s.Website(siteID)
	if err != nil || !ok {
		s.failJob(jobID, "backup_website_missing", "Backup failed", "Website was not found before backup started.", "Refresh the panel and retry backup.")
		return
	}
	setup, _ := s.settings.Load()
	result := s.performBackup(site, backupID, setup.BackupPath)
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		backup := findBackup(st, backupID)
		if job != nil {
			job.Status = JobDone
			if len(result.Issues) > 0 {
				job.Status = JobFailed
			}
			job.Steps = result.Steps
			job.Issues = result.Issues
			job.Logs = append(job.Logs, result.Logs...)
			job.UpdatedAt = now
		}
		if backup != nil {
			backup.Status = "completed"
			if len(result.Issues) > 0 {
				backup.Status = "failed"
			}
			backup.Path = result.Path
			backup.SizeBytes = result.SizeBytes
			backup.Issues = result.Issues
			backup.Logs = result.Logs
			backup.CompletedAt = &now
			backup.UpdatedAt = now
		}
		title := "Backup completed"
		tone := "mint"
		detail := site.Domain
		if len(result.Issues) > 0 {
			title = "Backup failed"
			tone = "rose"
			detail = firstIssueDetail(result.Issues, site.Domain)
		}
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "backup",
			Title:     title,
			Detail:    detail,
			Tone:      tone,
			CreatedAt: now,
		}}, st.Activity...)
		return nil
	})
}

func (s *Service) runRestoreJob(siteID, backupID, jobID string) {
	s.markGenericJobRunning(jobID)
	site, ok, err := s.Website(siteID)
	if err != nil || !ok {
		s.failJob(jobID, "restore_website_missing", "Restore failed", "Website was not found before restore started.", "Refresh the panel and retry restore.")
		return
	}
	st, err := s.store.Load()
	if err != nil {
		s.failJob(jobID, "restore_state_load_failed", "Restore failed", err.Error(), "Refresh the panel and retry restore.")
		return
	}
	backup := findBackup(&st, backupID)
	if backup == nil || backup.WebsiteID != siteID {
		s.failJob(jobID, "restore_backup_missing", "Restore failed", "Backup was not found before restore started.", "Choose another completed backup and retry restore.")
		return
	}
	spec := provisioning.RestoreBackupSpec{
		JobID:            jobID,
		SiteID:           site.ID,
		Domain:           site.Domain,
		SystemUser:       site.SystemUser,
		SystemGroup:      "cloudrifts-sites",
		HomeDir:          site.HomeDir,
		DocumentRoot:     site.DocumentRoot,
		BackupPath:       backup.Path,
		DatabaseName:     site.DatabaseName,
		DatabaseUser:     site.DatabaseUser,
		DatabasePassword: site.DatabasePassword,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, err := provisioning.RunRestoreBackup(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, spec)
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "restore_worker_crashed", Severity: "critical", Title: "Restore worker crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry the restore.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " restore worker crashed: " + err.Error()}}
	}
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		backup := findBackup(st, backupID)
		if job == nil || backup == nil {
			return nil
		}
		job.Steps = panelSteps(result.Steps)
		job.Issues = panelIssues(result.Issues)
		job.Logs = append(job.Logs, result.Logs...)
		job.Status = JobDone
		if result.Status == provisioning.ResultFailed {
			job.Status = JobFailed
		}
		job.UpdatedAt = now
		backup.LastRestoreJobID = job.ID
		backup.Issues = job.Issues
		backup.Logs = append(backup.Logs, result.Logs...)
		backup.UpdatedAt = now
		if site := findWebsite(st, siteID); site != nil {
			site.UpdatedAt = now
			if result.Status == provisioning.ResultFailed {
				site.Status = WebsiteAttention
				site.Healthy = false
				site.Issues = mergeProvisionIssues(site.Issues, job.Issues)
			} else {
				site.Issues = withoutIssuePrefix(site.Issues, "restore_")
			}
			site.NextActions = nextActions(*site)
		}
		title := "Backup restored"
		tone := "mint"
		if result.Status == provisioning.ResultFailed {
			title = "Backup restore failed"
			tone = "rose"
		}
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "backup",
			Title:     title,
			Detail:    backup.WebsiteDomain,
			Tone:      tone,
			CreatedAt: now,
		}}, st.Activity...)
		return nil
	})
}

type backupResult struct {
	Path      string
	SizeBytes int64
	Steps     []JobStep
	Issues    []WebsiteIssue
	Logs      []string
}

func (s *Service) performBackup(site Website, backupID string, backupRoot string) backupResult {
	now := time.Now().UTC()
	steps := []JobStep{}
	logs := []string{now.Format(time.RFC3339) + " backup worker started"}
	issues := []WebsiteIssue{}
	if backupRoot == "" || !strings.HasPrefix(backupRoot, "/") {
		backupRoot = filepath.Join(s.cfg.DataDir, "backups")
	}
	backupDir := filepath.Join(backupRoot, site.ID, backupID)
	archivePath := filepath.Join(backupDir, "files.tar.gz")
	dryRun := s.cfg.ExecutionMode == "dry-run" || runtime.GOOS != "linux"
	if dryRun {
		for _, step := range []JobStep{{ID: "files", Title: "Archive files", Detail: "Dry run: would archive " + site.DocumentRoot, Status: StepDone}, {ID: "database", Title: "Dump database", Detail: databaseBackupDetail(site), Status: StepDone}, {ID: "manifest", Title: "Write manifest", Detail: "Dry run: would write restore manifest", Status: StepDone}} {
			started := time.Now().UTC()
			step.StartedAt = &started
			step.EndedAt = &started
			steps = append(steps, step)
		}
		logs = append(logs, time.Now().UTC().Format(time.RFC3339)+" dry-run backup completed")
		return backupResult{Path: archivePath, SizeBytes: 0, Steps: steps, Issues: issues, Logs: logs}
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		issue := backupIssue("backup_directory_failed", err, "Check backup path ownership and retry.")
		return backupResult{Path: archivePath, Steps: failedBackupSteps(issue), Issues: []WebsiteIssue{issue}, Logs: append(logs, err.Error())}
	}
	fileStep := timedStep("files", "Archive files", "Archived "+site.DocumentRoot, StepDone, nil)
	if err := createTarGz(site.DocumentRoot, archivePath); err != nil {
		issue := backupIssue("backup_files_failed", err, "Check website file permissions and retry backup.")
		fileStep.Status = StepFailed
		fileStep.Issues = []WebsiteIssue{issue}
		issues = append(issues, issue)
	}
	steps = append(steps, fileStep)
	dbStep := timedStep("database", "Dump database", databaseBackupDetail(site), StepDone, nil)
	if site.App != AppPHP && site.DatabaseName != "" {
		dumpPath := filepath.Join(backupDir, "database.sql")
		if err := dumpDatabase(context.Background(), site, dumpPath); err != nil {
			issue := backupIssue("backup_database_failed", err, "Check database credentials and retry backup.")
			dbStep.Status = StepFailed
			dbStep.Issues = []WebsiteIssue{issue}
			issues = append(issues, issue)
		}
	}
	steps = append(steps, dbStep)
	manifestStep := timedStep("manifest", "Write manifest", "Wrote backup manifest", StepDone, nil)
	manifest := fmt.Sprintf("site=%s\ndomain=%s\ndocument_root=%s\ncreated_at=%s\n", site.ID, site.Domain, site.DocumentRoot, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(backupDir, "manifest.txt"), []byte(manifest), 0o640); err != nil {
		issue := backupIssue("backup_manifest_failed", err, "Check backup path ownership and retry.")
		manifestStep.Status = StepFailed
		manifestStep.Issues = []WebsiteIssue{issue}
		issues = append(issues, issue)
	}
	steps = append(steps, manifestStep)
	var size int64
	if info, err := os.Stat(archivePath); err == nil {
		size = info.Size()
	}
	logs = append(logs, time.Now().UTC().Format(time.RFC3339)+" backup finished at "+backupDir)
	return backupResult{Path: backupDir, SizeBytes: size, Steps: steps, Issues: issues, Logs: logs}
}

func (s *Service) ListFiles(id string, requestPath string) (FileList, bool, error) {
	site, ok, err := s.Website(id)
	if err != nil || !ok {
		return FileList{}, ok, err
	}
	full, rel, err := jailedPath(site.DocumentRoot, requestPath)
	if err != nil {
		return FileList{}, true, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && s.isDryRun() {
			now := time.Now().UTC()
			return FileList{Website: site, Path: rel, Entries: []FileEntry{{Name: "index.php", Path: "index.php", Type: "file", Size: 512, Mode: "-rw-r--r--", Modified: now}, {Name: ".well-known", Path: ".well-known", Type: "directory", Size: 0, Mode: "drwxr-xr-x", Modified: now}}}, true, nil
		}
		return FileList{}, true, err
	}
	out := []FileEntry{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		typeName := "file"
		if info.IsDir() {
			typeName = "directory"
		}
		childPath := strings.TrimPrefix(filepath.ToSlash(filepath.Join(rel, entry.Name())), "/")
		out = append(out, FileEntry{Name: entry.Name(), Path: childPath, Type: typeName, Size: info.Size(), Mode: info.Mode().Perm().String(), Modified: info.ModTime()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "directory"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return FileList{Website: site, Path: rel, Entries: out}, true, nil
}

func (s *Service) ReadFile(id string, requestPath string) (FileRead, bool, error) {
	site, ok, err := s.Website(id)
	if err != nil || !ok {
		return FileRead{}, ok, err
	}
	full, rel, err := jailedPath(site.DocumentRoot, requestPath)
	if err != nil {
		return FileRead{}, true, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && s.isDryRun() {
			content := "<?php\n// Cloudrifts dry-run preview for " + site.Domain + "\nphpinfo();\n"
			return FileRead{Website: site, Path: rel, Size: int64(len(content)), Content: content}, true, nil
		}
		return FileRead{}, true, err
	}
	if info.IsDir() {
		return FileRead{}, true, errors.New("path is a directory")
	}
	if info.Size() > 1024*1024 {
		return FileRead{}, true, errors.New("file is larger than the 1 MB editor limit")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return FileRead{}, true, err
	}
	return FileRead{Website: site, Path: rel, Size: info.Size(), Content: string(b)}, true, nil
}

func (s *Service) WriteFile(id string, input WriteFileRequest) (FileRead, bool, error) {
	site, ok, err := s.Website(id)
	if err != nil || !ok {
		return FileRead{}, ok, err
	}
	full, rel, err := jailedPath(site.DocumentRoot, input.Path)
	if err != nil {
		return FileRead{}, true, err
	}
	if len(input.Content) > 1024*1024 {
		return FileRead{}, true, errors.New("file content is larger than the 1 MB editor limit")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o775); err != nil {
		return FileRead{}, true, err
	}
	_ = os.Chmod(filepath.Dir(full), 0o2775)
	if err := os.WriteFile(full, []byte(input.Content), 0o664); err != nil {
		return FileRead{}, true, err
	}
	_ = os.Chmod(full, 0o664)
	return FileRead{Website: site, Path: rel, Size: int64(len(input.Content)), Content: input.Content}, true, nil
}

func (s *Service) Logs(id string, kind string, maxLines int) (WebsiteLogs, bool, error) {
	site, ok, err := s.Website(id)
	if err != nil || !ok {
		return WebsiteLogs{}, ok, err
	}
	if maxLines <= 0 || maxLines > 1000 {
		maxLines = 200
	}
	path := ""
	switch kind {
	case "access":
		path = filepath.Join(site.HomeDir, "logs", "access.log")
	case "php":
		path = filepath.Join(site.HomeDir, "logs", "php-error.log")
	default:
		kind = "error"
		path = filepath.Join(site.HomeDir, "logs", "error.log")
	}
	lines, err := tailLines(path, maxLines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WebsiteLogs{Website: site, Kind: kind, Lines: []string{}, Path: path}, true, nil
		}
		return WebsiteLogs{}, true, err
	}
	return WebsiteLogs{Website: site, Kind: kind, Lines: lines, Path: path}, true, nil
}

func (s *Service) isDryRun() bool {
	return s.cfg.ExecutionMode == "dry-run" || runtime.GOOS != "linux"
}

func findBackup(st *state, id string) *Backup {
	for i := range st.Backups {
		if st.Backups[i].ID == id {
			return &st.Backups[i]
		}
	}
	return nil
}

func jailedPath(root string, requestPath string) (string, string, error) {
	if root == "" {
		return "", "", errors.New("website document root is not set")
	}
	cleanRel := filepath.Clean("/" + strings.TrimSpace(requestPath))
	if cleanRel == "/" || cleanRel == "." {
		cleanRel = "/"
	}
	full := filepath.Join(root, strings.TrimPrefix(cleanRel, "/"))
	rootClean, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	fullClean, err := filepath.Abs(full)
	if err != nil {
		return "", "", err
	}
	if fullClean != rootClean && !strings.HasPrefix(fullClean, rootClean+string(os.PathSeparator)) {
		return "", "", errors.New("path escapes website root")
	}
	return fullClean, strings.TrimPrefix(filepath.ToSlash(cleanRel), "/"), nil
}

func createTarGz(sourceDir string, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
}

func dumpDatabase(ctx context.Context, site Website, target string) error {
	if site.DatabaseName == "" || site.DatabaseUser == "" || site.DatabasePassword == "" {
		return errors.New("database credentials are missing")
	}
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.CommandContext(ctx, "mysqldump", "--single-transaction", "--quick", "-u"+site.DatabaseUser, "-p"+site.DatabasePassword, site.DatabaseName)
	cmd.Stdout = out
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mysqldump failed: %s", strings.TrimSpace(string(b)))
	}
	return nil
}

func databaseBackupDetail(site Website) string {
	if site.App == AppPHP || site.DatabaseName == "" {
		return "No app database attached; database dump skipped"
	}
	return "Dump database " + site.DatabaseName
}

func backupIssue(code string, err error, fix string) WebsiteIssue {
	return WebsiteIssue{Code: code, Severity: "critical", Title: "Backup failed", Detail: err.Error(), Fix: fix, Retryable: true}
}

func failedBackupSteps(issue WebsiteIssue) []JobStep {
	now := time.Now().UTC()
	return []JobStep{{ID: "backup", Title: "Create backup", Detail: issue.Detail, Status: StepFailed, Issues: []WebsiteIssue{issue}, StartedAt: &now, EndedAt: &now}}
}

func timedStep(id, title, detail string, status JobStepStatus, issues []WebsiteIssue) JobStep {
	now := time.Now().UTC()
	return JobStep{ID: id, Title: title, Detail: detail, Status: status, Issues: issues, StartedAt: &now, EndedAt: &now}
}

func tailLines(path string, max int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(parts) == 1 && parts[0] == "" {
		return []string{}, nil
	}
	if len(parts) > max {
		parts = parts[len(parts)-max:]
	}
	return parts, nil
}
