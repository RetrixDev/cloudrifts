package provisioning

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
	"strings"
	"time"
)

type RestoreBackupSpec struct {
	JobID            string `json:"job_id"`
	SiteID           string `json:"site_id"`
	Domain           string `json:"domain"`
	SystemUser       string `json:"system_user"`
	SystemGroup      string `json:"system_group"`
	HomeDir          string `json:"home_dir"`
	DocumentRoot     string `json:"document_root"`
	BackupPath       string `json:"backup_path"`
	DatabaseName     string `json:"database_name,omitempty"`
	DatabaseUser     string `json:"database_user,omitempty"`
	DatabasePassword string `json:"database_password,omitempty"`
}

type restorer struct {
	ctx      context.Context
	spec     RestoreBackupSpec
	dryRun   bool
	steps    []StepResult
	issues   []Issue
	logs     []string
	rollback []string
	undo     []func()
	archive  string
	dump     string
	safety   string
	oldRoot  string
}

func ApplyRestoreBackup(ctx context.Context, spec RestoreBackupSpec, dryRun bool) Result {
	r := &restorer{ctx: ctx, spec: spec, dryRun: dryRun}
	if r.spec.SystemGroup == "" {
		r.spec.SystemGroup = siteGroup
	}
	r.log("starting restore for %s from %s", spec.Domain, spec.BackupPath)
	if !r.runStep("preflight", "Validate restore point", "Check backup archive, database dump, document root, and root helper before changing files", r.preflight) {
		return r.finish(ResultFailed)
	}
	if !r.runStep("safety", "Create safety snapshot", "Capture current website files before restore so failure can be recovered", r.createSafetySnapshot) {
		return r.finish(ResultFailed)
	}
	if !r.runStep("files", "Restore files", "Replace document root from the backup archive and preserve file modes", r.restoreFiles) {
		r.runRollback()
		return r.finish(ResultFailed)
	}
	if !r.runStep("database", "Restore database", "Rebuild and import the website database when the backup includes a dump", r.restoreDatabase) {
		r.runRollback()
		return r.finish(ResultFailed)
	}
	if !r.runStep("ownership", "Repair ownership", "Ensure restored files are owned by the website user and group", r.repairOwnership) {
		r.runRollback()
		return r.finish(ResultFailed)
	}
	return r.finish(ResultDone)
}

func (r *restorer) runStep(id, title, detail string, fn func() stepOutcome) bool {
	started := time.Now().UTC()
	step := StepResult{ID: id, Title: title, Detail: detail, Status: StepRunning, StartedAt: &started}
	r.log("%s: %s", id, title)
	out := fn()
	ended := time.Now().UTC()
	step.EndedAt = &ended
	if out.Detail != "" {
		step.Detail = out.Detail
	}
	step.Issues = out.Issues
	if out.Err != nil {
		issue := Issue{Code: id + "_failed", Severity: "critical", Title: title + " failed", Detail: out.Err.Error(), Fix: restoreRecoveryForStep(id), Retryable: true}
		step.Issues = append(step.Issues, issue)
		r.issues = append(r.issues, issue)
		step.Status = StepFailed
		r.log("%s failed: %s", id, out.Err.Error())
		r.steps = append(r.steps, step)
		return false
	}
	if out.Status == "" {
		out.Status = StepDone
	}
	step.Status = out.Status
	if len(out.Issues) > 0 {
		r.issues = append(r.issues, out.Issues...)
	}
	r.log("%s done", id)
	r.steps = append(r.steps, step)
	return true
}

func (r *restorer) preflight() stepOutcome {
	if r.spec.BackupPath == "" || r.spec.DocumentRoot == "" || r.spec.HomeDir == "" || r.spec.SystemUser == "" {
		return stepOutcome{Err: errors.New("restore spec is incomplete")}
	}
	if !strings.HasPrefix(filepath.Clean(r.spec.DocumentRoot), filepath.Clean(r.spec.HomeDir)+string(os.PathSeparator)) {
		return stepOutcome{Err: fmt.Errorf("document root %s must stay inside home %s", r.spec.DocumentRoot, r.spec.HomeDir)}
	}
	r.archive, r.dump = backupFilesForRestore(r.spec.BackupPath)
	if r.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Dry run: would restore %s for %s", r.archive, r.spec.Domain)}
	}
	if runtime.GOOS != "linux" {
		return stepOutcome{Err: fmt.Errorf("real restore is supported on Ubuntu Linux only, current OS is %s", runtime.GOOS)}
	}
	if os.Geteuid() != 0 {
		return stepOutcome{Err: errors.New("cloudrifts-agent must run as root to restore files and ownership")}
	}
	if !isUbuntu2404() {
		return stepOutcome{Err: errors.New("Cloudrifts Phase 1 supports Ubuntu 24.04 only")}
	}
	if _, err := os.Stat(r.archive); err != nil {
		return stepOutcome{Err: fmt.Errorf("backup archive is missing: %w", err)}
	}
	if r.spec.DatabaseName != "" {
		if _, err := os.Stat(r.dump); err != nil {
			return stepOutcome{Err: fmt.Errorf("database dump is missing: %w", err)}
		}
		if !safeSQLIdentifier(r.spec.DatabaseName) || !safeSQLIdentifier(r.spec.DatabaseUser) {
			return stepOutcome{Err: errors.New("database identifiers are unsafe")}
		}
		for _, cmd := range []string{"mysql"} {
			if _, err := exec.LookPath(cmd); err != nil {
				return stepOutcome{Err: fmt.Errorf("%s is missing; rerun the Cloudrifts installer", cmd)}
			}
		}
	}
	return stepOutcome{Detail: "Restore point is present and safe to apply"}
}

func (r *restorer) createSafetySnapshot() stepOutcome {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	r.safety = filepath.Join(filepath.Dir(r.spec.BackupPath), "pre-restore-"+r.spec.SiteID+"-"+stamp+".tar.gz")
	if r.dryRun {
		return stepOutcome{Detail: "Would create safety archive at " + r.safety}
	}
	if _, err := os.Stat(r.spec.DocumentRoot); errors.Is(err, os.ErrNotExist) {
		return stepOutcome{Detail: "Document root does not exist yet; safety snapshot skipped"}
	}
	if err := archiveDirectory(r.spec.DocumentRoot, r.safety); err != nil {
		return stepOutcome{Err: err}
	}
	r.rollback = append(r.rollback, "created safety archive "+r.safety)
	return stepOutcome{Detail: "Safety archive created at " + r.safety}
}

func (r *restorer) restoreFiles() stepOutcome {
	if r.dryRun {
		return stepOutcome{Detail: "Would replace " + r.spec.DocumentRoot + " from " + r.archive}
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if _, err := os.Stat(r.spec.DocumentRoot); err == nil {
		r.oldRoot = r.spec.DocumentRoot + ".cloudrifts-before-restore-" + stamp
		if err := os.Rename(r.spec.DocumentRoot, r.oldRoot); err != nil {
			return stepOutcome{Err: err}
		}
		r.undo = append(r.undo, func() {
			_ = os.RemoveAll(r.spec.DocumentRoot)
			_ = os.Rename(r.oldRoot, r.spec.DocumentRoot)
		})
		r.rollback = append(r.rollback, "moved current document root to "+r.oldRoot)
	}
	if err := os.MkdirAll(r.spec.DocumentRoot, 0o755); err != nil {
		return stepOutcome{Err: err}
	}
	if r.oldRoot == "" {
		r.undo = append(r.undo, func() { _ = os.RemoveAll(r.spec.DocumentRoot) })
	}
	if err := extractTarGz(r.archive, r.spec.DocumentRoot); err != nil {
		return stepOutcome{Err: err}
	}
	if r.oldRoot != "" {
		_ = os.RemoveAll(r.oldRoot)
	}
	return stepOutcome{Detail: "Files restored into " + r.spec.DocumentRoot}
}

func (r *restorer) restoreDatabase() stepOutcome {
	if r.spec.DatabaseName == "" {
		return stepOutcome{Detail: "No database attached to this website; database restore skipped"}
	}
	if r.dryRun {
		return stepOutcome{Detail: "Would rebuild and import database " + r.spec.DatabaseName}
	}
	reset := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'; ALTER USER '%s'@'localhost' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'; FLUSH PRIVILEGES;", r.spec.DatabaseName, r.spec.DatabaseName, r.spec.DatabaseUser, escapeSQLString(r.spec.DatabasePassword), r.spec.DatabaseUser, escapeSQLString(r.spec.DatabasePassword), r.spec.DatabaseName, r.spec.DatabaseUser)
	if err := r.runInput(reset, "mysql", "--protocol=socket", "-uroot"); err != nil {
		return stepOutcome{Err: err}
	}
	file, err := os.Open(r.dump)
	if err != nil {
		return stepOutcome{Err: err}
	}
	defer file.Close()
	cmd := exec.CommandContext(r.ctx, "mysql", "-u"+r.spec.DatabaseUser, "-p"+r.spec.DatabasePassword, r.spec.DatabaseName)
	cmd.Stdin = file
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		r.log("mysql import → %s", line)
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return stepOutcome{Err: fmt.Errorf("mysql import failed: %s", line)}
	}
	return stepOutcome{Detail: "Database " + r.spec.DatabaseName + " restored"}
}

func (r *restorer) repairOwnership() stepOutcome {
	if r.dryRun {
		return stepOutcome{Detail: fmt.Sprintf("Would chown restored files to %s:%s", r.spec.SystemUser, r.spec.SystemGroup)}
	}
	if err := r.run("chown", "-R", r.spec.SystemUser+":"+r.spec.SystemGroup, r.spec.DocumentRoot); err != nil {
		return stepOutcome{Err: err}
	}
	return stepOutcome{Detail: fmt.Sprintf("Ownership repaired to %s:%s", r.spec.SystemUser, r.spec.SystemGroup)}
}

func (r *restorer) runRollback() {
	if r.dryRun || len(r.undo) == 0 {
		return
	}
	r.log("restore rollback started")
	for i := len(r.undo) - 1; i >= 0; i-- {
		r.undo[i]()
	}
	r.log("restore rollback finished")
}

func (r *restorer) finish(defaultStatus ResultStatus) Result {
	status := defaultStatus
	for _, step := range r.steps {
		if step.Status == StepFailed {
			status = ResultFailed
			break
		}
		if step.Status == StepWarning && status != ResultFailed {
			status = ResultWarning
		}
	}
	r.log("restore finished with %s", status)
	return Result{Status: status, Steps: r.steps, Issues: r.issues, Logs: r.logs, Rollback: r.rollback, DryRun: r.dryRun}
}

func (r *restorer) run(name string, args ...string) error {
	cmd := exec.CommandContext(r.ctx, name, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		r.log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		r.log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func (r *restorer) runInput(input string, name string, args ...string) error {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if line != "" {
		r.log("%s %s → %s", name, strings.Join(args, " "), line)
	} else {
		r.log("%s %s", name, strings.Join(args, " "))
	}
	if err != nil {
		if line == "" {
			line = err.Error()
		}
		return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), line)
	}
	return nil
}

func (r *restorer) log(format string, args ...any) {
	r.logs = append(r.logs, time.Now().UTC().Format(time.RFC3339)+" "+fmt.Sprintf(format, args...))
}

func backupFilesForRestore(path string) (string, string) {
	if strings.HasSuffix(path, ".tar.gz") {
		dir := filepath.Dir(path)
		return path, filepath.Join(dir, "database.sql")
	}
	return filepath.Join(path, "files.tar.gz"), filepath.Join(path, "database.sql")
}

func archiveDirectory(sourceDir string, target string) error {
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

func extractTarGz(source string, targetDir string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean("/" + header.Name)
		if clean == "/" || strings.HasPrefix(clean, "/../") {
			return fmt.Errorf("unsafe path in archive: %s", header.Name)
		}
		target := filepath.Join(targetDir, strings.TrimPrefix(clean, "/"))
		if rel, err := filepath.Rel(targetDir, target); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("archive path escapes document root: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			continue
		}
	}
}

func restoreRecoveryForStep(id string) string {
	switch id {
	case "preflight":
		return "Pick a completed backup with files.tar.gz and database.sql present, then retry restore."
	case "safety":
		return "Fix backup storage permissions so Cloudrifts can write a pre-restore safety snapshot."
	case "files":
		return "Cloudrifts attempted to put the previous document root back. Check the job log and retry restore."
	case "database":
		return "Fix the database import error shown in the job log, then retry restore."
	case "ownership":
		return "Fix the Linux user/group shown in the job log, then retry ownership repair or restore."
	default:
		return "Fix the reported issue and retry restore."
	}
}
