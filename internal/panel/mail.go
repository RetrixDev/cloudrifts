package panel

import (
	"context"
	"os/exec"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
	"github.com/cloudrifts/panel/internal/settings"
)

func (s *Service) MailRole() (MailRoleState, error) {
	st, err := s.store.Load()
	if err != nil {
		return MailRoleState{}, err
	}
	setup, _ := s.settings.Load()
	return mailRoleState(st, setup), nil
}

func (s *Service) SetupMailRole() (SetupMailRoleResponse, error) {
	setup, err := s.settings.Load()
	if err != nil {
		return SetupMailRoleResponse{}, err
	}
	setup.EmailEnabled = true
	if setup.WebmailURL == "" {
		setup.WebmailURL = "webmail." + rootDomain(setup.PanelURL)
	}
	if _, err := s.settings.Save(setup); err != nil {
		return SetupMailRoleResponse{}, err
	}

	var out SetupMailRoleResponse
	_, err = s.store.Save(func(st *state) error {
		now := time.Now().UTC()
		job := queuedMailRoleJob(now)
		setStoredRole(st, ServerRole{ID: "mail", Name: "Email", Kind: "mail", Version: "managed", Status: "installing", Description: "Postfix and Dovecot are being configured", UpdatedAt: now})
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "mail",
			Title:     "Mail role setup queued",
			Detail:    mailHostname(setup),
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = SetupMailRoleResponse{Role: mailRoleState(*st, setup), Job: job}
		return nil
	})
	if err != nil {
		return out, err
	}
	if osEnvSyncJobs() {
		s.runMailRoleJob(out.Job.ID)
	} else {
		go s.runMailRoleJob(out.Job.ID)
	}
	return out, nil
}

func (s *Service) runMailRoleJob(jobID string) {
	s.markGenericJobRunning(jobID)
	setup, _ := s.settings.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, err := provisioning.RunSetupMailRole(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, provisioning.MailRoleSpec{JobID: jobID, Hostname: mailHostname(setup), WebmailURL: setup.WebmailURL, ServerIP: firstIPv4()})
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "mail_role_worker_crashed", Severity: "critical", Title: "Mail role worker crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry mail setup.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " mail role worker crashed: " + err.Error()}}
	}
	s.applyMailRoleResult(jobID, result)
}

func (s *Service) applyMailRoleResult(jobID string, result provisioning.Result) {
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		if job := findJob(st, jobID); job != nil {
			job.Steps = panelSteps(result.Steps)
			job.Issues = panelIssues(result.Issues)
			job.Logs = append(job.Logs, result.Logs...)
			job.Status = JobDone
			if result.Status == provisioning.ResultFailed {
				job.Status = JobFailed
			}
			job.UpdatedAt = now
		}
		status := "ready"
		title := "Mail role ready"
		tone := "mint"
		description := "Postfix and Dovecot are ready for SMTP and IMAP"
		if result.Status == provisioning.ResultFailed {
			status = "failed"
			title = "Mail role setup failed"
			tone = "rose"
			description = "Mail setup needs attention before mailbox features can be enabled"
		}
		setStoredRole(st, ServerRole{ID: "mail", Name: "Email", Kind: "mail", Version: "managed", Status: status, Description: description, UpdatedAt: now})
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "mail",
			Title:     title,
			Detail:    firstIssueDetail(panelIssues(result.Issues), "Postfix and Dovecot"),
			Tone:      tone,
			CreatedAt: now,
		}}, st.Activity...)
		return nil
	})
}

func queuedMailRoleJob(now time.Time) Job {
	return Job{ID: "job-" + token(6), Type: "server.mail.setup", Title: "Setup mail role", TargetID: "server:mail", TargetName: "Email", Status: JobQueued, Steps: []JobStep{
		{ID: "validate", Title: "Validate mail role request", Detail: "Check Ubuntu, root helper, Postfix, Dovecot and systemd", Status: StepPending},
		{ID: "postfix", Title: "Configure Postfix", Detail: "Set hostname, TLS defaults, Maildir delivery and SMTP auth handoff", Status: StepPending},
		{ID: "dovecot", Title: "Configure Dovecot", Detail: "Set IMAP, Maildir and Postfix auth socket", Status: StepPending},
		{ID: "service", Title: "Start mail services", Detail: "Enable and restart Postfix and Dovecot", Status: StepPending},
		{ID: "firewall", Title: "Open mail firewall", Detail: "Open SMTP, submission and IMAP ports when UFW is active", Status: StepPending},
	}, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " mail role setup queued"}, CreatedAt: now, UpdatedAt: now}
}

func mailRoleState(st state, setup settings.SetupSettings) MailRoleState {
	status := "disabled"
	ready := false
	if setup.EmailEnabled {
		status = "not_configured"
	}
	if role := findRole(&st, "mail"); role != nil {
		status = role.Status
		ready = role.Status == "ready" || role.Status == "running"
	}
	if !setup.EmailEnabled {
		status = "disabled"
		ready = false
	}
	return MailRoleState{Enabled: setup.EmailEnabled, Status: status, Installed: mailInstalled(), Ready: ready, Hostname: mailHostname(setup), WebmailURL: setup.WebmailURL, ServerIP: firstIPv4(), UpdatedAt: st.Updated}
}

func mailHostname(setup settings.SetupSettings) string {
	root := rootDomain(setup.PanelURL)
	if root == "" {
		root = rootDomain(setup.WebmailURL)
	}
	if root == "" {
		root = "cloudrifts.local"
	}
	return "mail." + root
}

func mailInstalled() bool {
	for _, name := range []string{"postconf", "postfix", "doveconf"} {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}
