package panel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
)

func (s *Service) SwitchWebserver(target string) (SwitchWebserverResponse, error) {
	to, err := canonicalWebserver(target)
	if err != nil {
		return SwitchWebserverResponse{}, err
	}
	setup, err := s.settings.Load()
	if err != nil {
		return SwitchWebserverResponse{}, err
	}
	from, _ := canonicalWebserver(setup.Webserver)
	var out SwitchWebserverResponse
	_, err = s.store.Save(func(st *state) error {
		now := time.Now().UTC()
		job := queuedSwitchWebserverJob(from, to, st.Websites, now)
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "server",
			Title:     "Webserver switch queued",
			Detail:    fmt.Sprintf("%s → %s", activeWebserverName(from), activeWebserverName(to)),
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = SwitchWebserverResponse{From: from, To: to, Job: job}
		return nil
	})
	if err != nil {
		return out, err
	}
	if os.Getenv("CLOUDRIFTS_SYNC_JOBS") == "1" {
		s.runSwitchWebserverJob(out.Job.ID, from, to)
	} else {
		go s.runSwitchWebserverJob(out.Job.ID, from, to)
	}
	out.Webservers, _ = s.Webservers()
	return out, nil
}

func queuedSwitchWebserverJob(from string, to string, sites []Website, now time.Time) Job {
	count := len(sites)
	return Job{
		ID:         "job-" + token(6),
		Type:       "server.webserver.switch",
		Title:      "Switch webserver",
		TargetID:   "server:webserver",
		TargetName: activeWebserverName(to),
		Status:     JobQueued,
		Steps: []JobStep{
			{ID: "preflight", Title: "Validate target webserver", Detail: fmt.Sprintf("Check %s installation and %d website records", activeWebserverName(to), count), Status: StepPending},
			{ID: "render", Title: "Render target configs", Detail: fmt.Sprintf("Generate %s virtual hosts for every website", activeWebserverName(to)), Status: StepPending},
			{ID: "validate", Title: "Validate target config", Detail: "Run config test before moving traffic", Status: StepPending},
			{ID: "activate", Title: "Activate target webserver", Detail: fmt.Sprintf("Move traffic from %s to %s with rollback", activeWebserverName(from), activeWebserverName(to)), Status: StepPending},
			{ID: "settings", Title: "Save server setting", Detail: "Persist the active webserver only after activation succeeds", Status: StepPending},
		},
		Issues:    []WebsiteIssue{},
		Logs:      []string{now.Format(time.RFC3339) + " webserver switch queued"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *Service) runSwitchWebserverJob(jobID string, from string, to string) {
	s.markGenericJobRunning(jobID)
	st, err := s.store.Load()
	if err != nil {
		s.failJob(jobID, "switch_state_load_failed", "Webserver switch failed", err.Error(), "Refresh the panel and retry the switch.")
		return
	}
	spec := provisioning.SwitchWebserverSpec{JobID: jobID, From: from, To: to, Websites: make([]provisioning.WebsiteSpec, 0, len(st.Websites))}
	for _, site := range st.Websites {
		ws := s.provisionSpec(jobID, site)
		ws.Webserver = to
		spec.Websites = append(spec.Websites, ws)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, err := provisioning.RunSwitchWebserver(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, spec)
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "webserver_switch_crashed", Severity: "critical", Title: "Webserver switch crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry the webserver switch.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " webserver switch crashed: " + err.Error()}}
	}
	s.applySwitchWebserverResult(jobID, from, to, result)
}

func (s *Service) markGenericJobRunning(jobID string) {
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		if j := findJob(st, jobID); j != nil {
			j.Status = JobRunning
			j.UpdatedAt = now
			j.Logs = append(j.Logs, now.Format(time.RFC3339)+" worker started")
			if len(j.Steps) > 0 && j.Steps[0].Status == StepPending {
				j.Steps[0].Status = StepRunning
				j.Steps[0].StartedAt = &now
			}
		}
		return nil
	})
}

func (s *Service) applySwitchWebserverResult(jobID string, from string, to string, result provisioning.Result) {
	now := time.Now().UTC()
	settingsIssue := WebsiteIssue{}
	settingsFailed := false
	if result.Status != provisioning.ResultFailed {
		setup, err := s.settings.Load()
		if err != nil {
			settingsFailed = true
			settingsIssue = WebsiteIssue{Code: "webserver_setting_load_failed", Severity: "critical", Title: "Could not save active webserver", Detail: err.Error(), Fix: "Check /var/lib/cloudrifts ownership, then retry saving server settings.", Retryable: true}
		} else {
			setup.Webserver = to
			if _, err := s.settings.Save(setup); err != nil {
				settingsFailed = true
				settingsIssue = WebsiteIssue{Code: "webserver_setting_save_failed", Severity: "critical", Title: "Could not save active webserver", Detail: err.Error(), Fix: "Check /var/lib/cloudrifts ownership, then retry saving server settings.", Retryable: true}
			}
		}
	}
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		if job != nil {
			job.Steps = panelSteps(result.Steps)
			job.Issues = panelIssues(result.Issues)
			job.Logs = append(job.Logs, result.Logs...)
			job.Status = JobDone
			if result.Status == provisioning.ResultFailed || settingsFailed {
				job.Status = JobFailed
			}
			settingsStep := JobStep{ID: "settings", Title: "Save server setting", Detail: "Active webserver saved as " + activeWebserverName(to), Status: StepDone, StartedAt: &now, EndedAt: &now}
			if result.Status == provisioning.ResultFailed {
				settingsStep.Detail = "Skipped because activation did not complete"
				settingsStep.Status = StepPending
			}
			if settingsFailed {
				settingsStep.Detail = settingsIssue.Detail
				settingsStep.Status = StepFailed
				settingsStep.Issues = []WebsiteIssue{settingsIssue}
				job.Issues = append(job.Issues, settingsIssue)
				job.Logs = append(job.Logs, now.Format(time.RFC3339)+" "+settingsIssue.Detail)
			}
			job.Steps = append(job.Steps, settingsStep)
			job.UpdatedAt = now
		}
		if result.Status != provisioning.ResultFailed && !settingsFailed {
			for i := range st.Websites {
				st.Websites[i].WebserverStatus = "running"
				st.Websites[i].UpdatedAt = now
			}
		}
		title := "Webserver switch completed"
		tone := "mint"
		if result.Status == provisioning.ResultFailed || settingsFailed {
			title = "Webserver switch failed"
			tone = "rose"
		}
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "server",
			Title:     title,
			Detail:    fmt.Sprintf("%s → %s", activeWebserverName(from), activeWebserverName(to)),
			Tone:      tone,
			CreatedAt: now,
		}}, st.Activity...)
		return nil
	})
}

func canonicalWebserver(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "nginx":
		return "nginx", nil
	case "apache", "apache2":
		return "apache", nil
	case "openlitespeed", "ols":
		return "openlitespeed", nil
	case "litespeed", "lsws":
		return "litespeed", nil
	default:
		return "", errors.New("webserver must be nginx, apache, openlitespeed, or litespeed")
	}
}
