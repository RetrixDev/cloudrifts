package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func RunCreateWebsite(ctx context.Context, opts RunOptions, spec WebsiteSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplyCreateWebsite(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "apply-website", spec.JobID+"-website.json", spec)
}

func RunSwitchWebserver(ctx context.Context, opts RunOptions, spec SwitchWebserverSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplySwitchWebserver(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "switch-webserver", spec.JobID+"-webserver-switch.json", spec)
}

func RunRestoreBackup(ctx context.Context, opts RunOptions, spec RestoreBackupSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplyRestoreBackup(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "restore-backup", spec.JobID+"-restore.json", spec)
}

func RunSetupDNSRole(ctx context.Context, opts RunOptions, spec DNSRoleSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplySetupDNSRole(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "setup-dns-role", spec.JobID+"-dns-role.json", spec)
}

func RunApplyDNSZone(ctx context.Context, opts RunOptions, spec DNSZoneSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplyDNSZone(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "apply-dns-zone", spec.JobID+"-dns-zone.json", spec)
}

func RunSetupMailRole(ctx context.Context, opts RunOptions, spec MailRoleSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplySetupMailRole(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "setup-mail-role", spec.JobID+"-mail-role.json", spec)
}

func RunSetupPanel(ctx context.Context, opts RunOptions, spec PanelSpec) (Result, error) {
	mode := resolveMode(opts)
	if mode == ModeDryRun {
		return ApplySetupPanel(ctx, spec, true), nil
	}
	return invokeAgent(ctx, opts, "setup-panel", spec.JobID+"-panel.json", spec)
}

func resolveMode(opts RunOptions) Mode {
	mode := opts.Mode
	if mode == "" {
		mode = ModeAuto
	}
	if mode == ModeDryRun || mode == ModeReal {
		return mode
	}
	if runtime.GOOS != "linux" {
		return ModeDryRun
	}
	agent := opts.AgentPath
	if agent == "" {
		agent = "/usr/local/bin/cloudrifts-agent"
	}
	if _, err := os.Stat(agent); err == nil {
		return ModeReal
	}
	return ModeDryRun
}

func invokeAgent(ctx context.Context, opts RunOptions, command string, specFileName string, spec any) (Result, error) {
	agent := opts.AgentPath
	if agent == "" {
		agent = "/usr/local/bin/cloudrifts-agent"
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = "/var/lib/cloudrifts"
	}
	jobsDir := filepath.Join(dataDir, "jobs")
	if err := os.MkdirAll(jobsDir, 0o750); err != nil {
		return failureFromError("agent_spec_write_failed", "Could not prepare job directory", err, "Check ownership of "+jobsDir+" and restart cloudriftsd."), nil
	}
	specPath := filepath.Join(jobsDir, specFileName)
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(specPath, b, 0o640); err != nil {
		return failureFromError("agent_spec_write_failed", "Could not write job spec", err, "Check ownership of "+jobsDir+" and restart cloudriftsd."), nil
	}

	args := []string{command, "--spec", specPath, "--json"}
	cmdPath := agent
	if os.Geteuid() != 0 {
		sudo := opts.SudoPath
		if sudo == "" {
			sudo = "/usr/bin/sudo"
		}
		cmdPath = sudo
		args = append([]string{"-n", agent}, args...)
	}

	cmd := exec.CommandContext(ctx, cmdPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return failureFromError("agent_execution_failed", "Cloudrifts agent could not finish the job", errors.New(detail), "Run journalctl -u cloudriftsd -n 120 --no-pager, then retry the job from the panel."), nil
	}
	var result Result
	if err := json.Unmarshal(out, &result); err != nil {
		return failureFromError("agent_bad_response", "Cloudrifts agent returned unreadable output", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))), "Restart cloudriftsd and retry. If it repeats, reinstall the Cloudrifts bundle."), nil
	}
	return result, nil
}

func failureFromError(code string, title string, err error, fix string) Result {
	now := time.Now().UTC()
	issue := Issue{Code: code, Severity: "critical", Title: title, Detail: err.Error(), Fix: fix, Retryable: true}
	return Result{
		Status: ResultFailed,
		Steps:  []StepResult{{ID: "agent", Title: title, Detail: err.Error(), Status: StepFailed, Issues: []Issue{issue}, StartedAt: &now, EndedAt: &now}},
		Issues: []Issue{issue},
		Logs:   []string{now.Format(time.RFC3339) + " " + title + ": " + err.Error()},
	}
}
