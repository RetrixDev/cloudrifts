package panel

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
	"github.com/cloudrifts/panel/internal/settings"
)

type DelegationRecord struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Where  string `json:"where"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type SetupApplyResponse struct {
	Settings          settings.SetupSettings `json:"settings"`
	Job               Job                    `json:"job"`
	Role              DNSRoleState           `json:"role"`
	Zone              *DNSZone               `json:"zone,omitempty"`
	DelegationRecords []DelegationRecord     `json:"delegation_records"`
}

type SetupDNSCheckResponse struct {
	PanelURL        string             `json:"panel_url"`
	RootDomain      string             `json:"root_domain"`
	ServerIP        string             `json:"server_ip"`
	DNSMode         string             `json:"dns_mode"`
	Status          string             `json:"status"`
	Title           string             `json:"title"`
	Detail          string             `json:"detail"`
	PanelResolved   []string           `json:"panel_resolved"`
	Nameservers     []string           `json:"nameservers"`
	PanelRecordOK   bool               `json:"panel_record_ok"`
	DelegationOK    bool               `json:"delegation_ok"`
	NameserverAOK   bool               `json:"nameserver_a_ok"`
	RequiredRecords []DelegationRecord `json:"required_records"`
	CheckedAt       time.Time          `json:"checked_at"`
}

func (s *Service) ApplySetup(input settings.SetupSettings) (SetupApplyResponse, error) {
	input = settings.Normalize(input)
	if input.PanelURL == "" {
		return SetupApplyResponse{}, fmt.Errorf("panel_url is required")
	}
	if input.DNSMode == "managed" {
		input.Nameserver1 = emptyAs(input.Nameserver1, "ns1."+settings.RootDomain(input.PanelURL))
		input.Nameserver2 = emptyAs(input.Nameserver2, "ns2."+settings.RootDomain(input.PanelURL))
	}
	input.PanelSSLStatus = "pending"
	input.PanelSSLReason = "Setup job is preparing DNS, panel route and SSL."
	saved, err := s.settings.Save(input)
	if err != nil {
		return SetupApplyResponse{}, err
	}

	var out SetupApplyResponse
	_, err = s.store.Save(func(st *state) error {
		now := time.Now().UTC()
		job := queuedSetupJob(saved, now)
		saved.LastSetupJobID = job.ID
		zone := ensureSetupDNSZone(st, saved, now)
		if zone != nil {
			saved.ManagedDNSZone = zone.ID
		}
		setStoredRole(st, setupDNSRoleForSettings(saved, now))
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "settings",
			Title:     "Setup started",
			Detail:    saved.PanelURL,
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = SetupApplyResponse{Settings: saved, Job: job, Role: dnsRoleState(*st, saved), DelegationRecords: setupDelegationRecords(saved, firstIPv4())}
		if zone != nil {
			z := *zone
			out.Zone = &z
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if _, err := s.settings.Save(saved); err != nil {
		return out, err
	}
	if osEnvSyncJobs() {
		s.runSetupJob(out.Job.ID)
	} else {
		go s.runSetupJob(out.Job.ID)
	}
	return out, nil
}

func (s *Service) runSetupJob(jobID string) {
	s.markGenericJobRunning(jobID)
	setup, err := s.settings.Load()
	if err != nil {
		s.failJob(jobID, "setup_settings_load_failed", "Setup failed", err.Error(), "Refresh setup and retry.")
		return
	}
	st, err := s.store.Load()
	if err != nil {
		s.failJob(jobID, "setup_state_load_failed", "Setup failed", err.Error(), "Refresh setup and retry.")
		return
	}
	var zone *DNSZone
	if setup.ManagedDNSZone != "" {
		zone = findDNSZone(&st, setup.ManagedDNSZone)
	}
	if zone == nil && setup.DNSMode == "managed" {
		zone = findDNSZoneByDomain(&st, settings.RootDomain(setup.PanelURL))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	results := []namedResult{}
	if setup.DNSMode == "managed" {
		dnsRoleResult, err := provisioning.RunSetupDNSRole(ctx, provisioning.RunOptions{Mode: provisioning.Mode(s.cfg.ExecutionMode), AgentPath: s.cfg.AgentPath, SudoPath: s.cfg.SudoPath, DataDir: s.cfg.DataDir}, provisioning.DNSRoleSpec{JobID: jobID, ServerIP: firstIPv4(), Nameserver1: setup.Nameserver1, Nameserver2: setup.Nameserver2})
		if err != nil {
			dnsRoleResult = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "setup_dns_role_crashed", Severity: "critical", Title: "DNS role crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry setup.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " DNS role crashed: " + err.Error()}}
		}
		results = append(results, namedResult{Name: "Managed DNS role", Prefix: "dns_role", Result: dnsRoleResult})
		if dnsRoleResult.Status != provisioning.ResultFailed && zone != nil {
			spec := provisioning.DNSZoneSpec{JobID: jobID, ZoneID: zone.ID, Domain: zone.Domain, ServerIP: zone.ServerIP, Nameserver1: zone.Nameserver1, Nameserver2: zone.Nameserver2, Records: make([]provisioning.DNSRecordSpec, 0, len(zone.Records))}
			for _, record := range zone.Records {
				spec.Records = append(spec.Records, provisioning.DNSRecordSpec{Name: record.Name, Type: record.Type, Content: record.Value, TTL: record.TTL})
			}
			dnsZoneResult, err := provisioning.RunApplyDNSZone(ctx, provisioning.RunOptions{Mode: provisioning.Mode(s.cfg.ExecutionMode), AgentPath: s.cfg.AgentPath, SudoPath: s.cfg.SudoPath, DataDir: s.cfg.DataDir}, spec)
			if err != nil {
				dnsZoneResult = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "setup_dns_zone_crashed", Severity: "critical", Title: "DNS zone crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry setup.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " DNS zone crashed: " + err.Error()}}
			}
			results = append(results, namedResult{Name: "Managed DNS records", Prefix: "dns_zone", Result: dnsZoneResult})
		}
	}
	panelResult, err := provisioning.RunSetupPanel(ctx, provisioning.RunOptions{Mode: provisioning.Mode(s.cfg.ExecutionMode), AgentPath: s.cfg.AgentPath, SudoPath: s.cfg.SudoPath, DataDir: s.cfg.DataDir}, provisioning.PanelSpec{JobID: jobID, PanelURL: setup.PanelURL, ServerIP: firstIPv4(), BackendURL: "http://127.0.0.1:8080", ACMEWebroot: "/var/lib/cloudrifts/acme"})
	if err != nil {
		panelResult = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "setup_panel_crashed", Severity: "critical", Title: "Panel route crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry setup.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " panel route crashed: " + err.Error()}}
	}
	results = append(results, namedResult{Name: "Panel route and SSL", Prefix: "panel", Result: panelResult})
	s.applySetupResults(jobID, setup, zone, results)
}

type namedResult struct {
	Name   string
	Prefix string
	Result provisioning.Result
}

func (s *Service) applySetupResults(jobID string, setup settings.SetupSettings, zone *DNSZone, results []namedResult) {
	now := time.Now().UTC()
	steps := []JobStep{}
	issues := []WebsiteIssue{}
	logs := []string{}
	failed := false
	trustedSSL := false
	panelWarning := ""
	for _, item := range results {
		steps = append(steps, prefixedPanelSteps(item.Prefix, item.Name, item.Result.Steps)...)
		itemIssues := panelIssues(item.Result.Issues)
		issues = append(issues, itemIssues...)
		logs = append(logs, item.Result.Logs...)
		if item.Result.Status == provisioning.ResultFailed {
			failed = true
		}
		if item.Prefix == "panel" {
			trustedSSL = item.Result.SSLIssued
			if len(itemIssues) > 0 {
				panelWarning = firstIssueDetail(itemIssues, "Panel SSL needs attention")
			}
		}
	}
	if len(steps) == 0 {
		steps = []JobStep{{ID: "setup", Title: "Apply setup", Detail: "No worker details returned", Status: StepFailed, StartedAt: &now, EndedAt: &now}}
		failed = true
	}
	setup.LastSetupJobID = jobID
	setup.SetupCompleted = !failed
	if trustedSSL {
		setup.PanelSSLStatus = "active"
		setup.PanelSSLReason = "Let's Encrypt certificate is active for " + setup.PanelURL
	} else if failed {
		setup.PanelSSLStatus = "failed"
		setup.PanelSSLReason = firstIssueDetail(issues, "Setup failed before panel SSL became active")
	} else if panelWarning != "" {
		if strings.Contains(strings.ToLower(panelWarning), "delegat") || strings.Contains(strings.ToLower(panelWarning), "dns") {
			setup.PanelSSLStatus = "needs_dns"
		} else {
			setup.PanelSSLStatus = "self_signed"
		}
		setup.PanelSSLReason = panelWarning
	} else {
		setup.PanelSSLStatus = "self_signed"
		setup.PanelSSLReason = "Panel HTTPS fallback is active; retry trusted SSL after DNS propagation."
	}
	if zone != nil {
		setup.ManagedDNSZone = zone.ID
	}
	_, _ = s.settings.Save(setup)
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		if job != nil {
			job.Steps = steps
			job.Issues = issuesOrEmpty(issues)
			job.Logs = append(job.Logs, logs...)
			job.Status = JobDone
			if failed {
				job.Status = JobFailed
			}
			job.UpdatedAt = now
		}
		if setup.DNSMode == "managed" {
			status := "ready"
			detail := "PowerDNS is configured and setup zone records are applied"
			if failed {
				status = "needs_attention"
				detail = "Managed DNS setup needs attention before delegation is safe"
			}
			setStoredRole(st, ServerRole{ID: "dns", Name: "PowerDNS", Kind: "dns", Version: "managed", Status: status, Description: detail, UpdatedAt: now})
			if zone != nil {
				if live := findDNSZone(st, zone.ID); live != nil {
					live.Issues = issuesForPrefix(issues, "dns_")
					live.UpdatedAt = now
					if failed && len(live.Issues) > 0 {
						live.Status = "failed"
					} else {
						live.Status = "active"
						live.LastAppliedAt = &now
					}
					for i := range live.Records {
						live.Records[i].UpdatedAt = now
						if live.Status == "active" {
							live.Records[i].Status = "active"
							live.Records[i].Reason = "Applied to PowerDNS during setup"
						} else {
							live.Records[i].Status = "failed"
							live.Records[i].Reason = firstIssueDetail(live.Issues, "DNS setup failed")
						}
					}
				}
			}
		}
		title := "Setup completed"
		tone := "mint"
		detail := setup.PanelURL
		if failed {
			title = "Setup failed"
			tone = "rose"
			detail = firstIssueDetail(issues, setup.PanelURL)
		} else if len(issues) > 0 {
			title = "Setup needs DNS propagation"
			tone = "amber"
			detail = firstIssueDetail(issues, setup.PanelURL)
		}
		st.Activity = append([]ActivityEvent{{ID: "act-" + token(5), Icon: "settings", Title: title, Detail: detail, Tone: tone, CreatedAt: now}}, st.Activity...)
		return nil
	})
}

func queuedSetupJob(setup settings.SetupSettings, now time.Time) Job {
	steps := []JobStep{
		{ID: "validate", Title: "Validate setup", Detail: "Check panel URL, service URLs, nameservers, server IP and selected roles", Status: StepPending},
		{ID: "dns_role", Title: "Prepare managed DNS", Detail: dnsSetupStepDetail(setup), Status: StepPending},
		{ID: "dns_records", Title: "Apply setup DNS records", Detail: "Create panel, nameserver, webmail, phpMyAdmin and mail records", Status: StepPending},
		{ID: "delegation", Title: "Prepare registrar delegation", Detail: "Show exact child nameserver glue and nameserver values", Status: StepPending},
		{ID: "panel_route", Title: "Install panel route", Detail: "Create Nginx HTTP/HTTPS reverse proxy to Cloudrifts", Status: StepPending},
		{ID: "panel_ssl", Title: "Issue panel SSL", Detail: "Try Let's Encrypt and keep a fallback HTTPS route if DNS is still propagating", Status: StepPending},
	}
	return Job{ID: "job-" + token(6), Type: "setup.panel.configure", Title: "Apply panel setup", TargetID: "setup", TargetName: setup.PanelURL, Status: JobQueued, Steps: steps, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " setup queued for " + setup.PanelURL}, CreatedAt: now, UpdatedAt: now}
}

func dnsSetupStepDetail(setup settings.SetupSettings) string {
	if setup.DNSMode == "managed" {
		return "Install PowerDNS and serve " + settings.RootDomain(setup.PanelURL) + " from " + setup.Nameserver1 + " / " + setup.Nameserver2
	}
	return "External DNS selected; Cloudrifts will show required records but will not install PowerDNS"
}

func ensureSetupDNSZone(st *state, setup settings.SetupSettings, now time.Time) *DNSZone {
	if setup.DNSMode != "managed" {
		return nil
	}
	root := settings.RootDomain(setup.PanelURL)
	if root == "" {
		return nil
	}
	records := setupDNSZoneRecords(setup, firstIPv4(), now)
	zone := findDNSZoneByDomain(st, root)
	if zone == nil {
		st.DNSZones = append([]DNSZone{{ID: "zone-" + token(5), Domain: root, Mode: "managed", Status: "queued", ServerIP: firstIPv4(), Nameserver1: setup.Nameserver1, Nameserver2: setup.Nameserver2, Records: records, Issues: []WebsiteIssue{}, CreatedAt: now, UpdatedAt: now}}, st.DNSZones...)
		return &st.DNSZones[0]
	}
	zone.Mode = "managed"
	zone.Status = "queued"
	zone.ServerIP = firstIPv4()
	zone.Nameserver1 = setup.Nameserver1
	zone.Nameserver2 = setup.Nameserver2
	zone.Records = mergeDNSRecords(zone.Records, records)
	zone.Issues = []WebsiteIssue{}
	zone.UpdatedAt = now
	return zone
}

func setupDNSZoneRecords(setup settings.SetupSettings, serverIP string, now time.Time) []DNSZoneRecord {
	root := settings.RootDomain(setup.PanelURL)
	seen := map[string]bool{}
	var out []DNSZoneRecord
	add := func(recordType, host, value, reason string, ttl int) {
		name := relativeDNSName(root, host)
		if name == "" || value == "" {
			return
		}
		key := strings.ToUpper(recordType) + "\x00" + strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, DNSZoneRecord{ID: "record-" + token(5), Type: strings.ToUpper(recordType), Name: name, Value: value, TTL: ttlOrDefault(ttl), Status: "queued", Reason: reason, CreatedAt: now, UpdatedAt: now})
	}
	add("A", root, serverIP, "Apex points to this Cloudrifts server", 300)
	add("A", setup.PanelURL, serverIP, "Panel URL points to this Cloudrifts server", 300)
	add("A", setup.Nameserver1, serverIP, "Child nameserver glue target", 300)
	add("A", setup.Nameserver2, serverIP, "Child nameserver glue target", 300)
	add("A", setup.WebmailURL, serverIP, "Webmail endpoint points to this server", 300)
	add("A", setup.PhpMyAdminURL, serverIP, "phpMyAdmin endpoint points to this server", 300)
	add("A", "mail."+root, serverIP, "Mail host points to this server", 300)
	add("MX", root, "10 mail."+root, "Default mail exchanger for the domain", 300)
	return out
}

func relativeDNSName(zone, host string) string {
	zone = strings.Trim(strings.ToLower(zone), ".")
	host = strings.Trim(strings.ToLower(host), ".")
	if zone == "" || host == "" {
		return ""
	}
	if host == zone {
		return "@"
	}
	if strings.HasSuffix(host, "."+zone) {
		return strings.TrimSuffix(host, "."+zone)
	}
	return ""
}

func ttlOrDefault(ttl int) int {
	if ttl <= 0 {
		return 300
	}
	return ttl
}

func setupDelegationRecords(setup settings.SetupSettings, serverIP string) []DelegationRecord {
	root := settings.RootDomain(setup.PanelURL)
	if root == "" {
		root = setup.PanelURL
	}
	return []DelegationRecord{
		{Type: "GLUE", Name: setup.Nameserver1, Value: serverIP, Where: "Registrar child nameserver / host records", Status: "required", Detail: "Create child nameserver glue before changing nameservers."},
		{Type: "GLUE", Name: setup.Nameserver2, Value: serverIP, Where: "Registrar child nameserver / host records", Status: "required", Detail: "For phase 1 both ns1 and ns2 can point to the same server IP."},
		{Type: "NS", Name: root, Value: setup.Nameserver1, Where: "Registrar nameserver delegation", Status: "required", Detail: "Set domain nameserver 1 to this value."},
		{Type: "NS", Name: root, Value: setup.Nameserver2, Where: "Registrar nameserver delegation", Status: "required", Detail: "Set domain nameserver 2 to this value."},
		{Type: "A", Name: setup.PanelURL, Value: serverIP, Where: "Cloudrifts managed DNS zone", Status: "managed", Detail: "Cloudrifts applies this record inside PowerDNS."},
	}
}

func setupDNSRoleForSettings(setup settings.SetupSettings, now time.Time) ServerRole {
	if setup.DNSMode == "managed" {
		return ServerRole{ID: "dns", Name: "PowerDNS", Kind: "dns", Version: "managed", Status: "installing", Description: "Authoritative DNS role is being configured for setup", UpdatedAt: now}
	}
	return ServerRole{ID: "dns", Name: "External DNS", Kind: "dns", Version: "external", Status: "external", Description: "DNS records are managed outside Cloudrifts", UpdatedAt: now}
}

func prefixedPanelSteps(prefix, group string, in []provisioning.StepResult) []JobStep {
	out := make([]JobStep, 0, len(in)+1)
	if len(in) == 0 {
		return []JobStep{{ID: prefix, Title: group, Detail: "Worker did not return step details", Status: StepFailed}}
	}
	for _, step := range in {
		out = append(out, JobStep{ID: prefix + "." + step.ID, Title: group + " · " + step.Title, Detail: step.Detail, Status: JobStepStatus(step.Status), Issues: panelIssues(step.Issues), StartedAt: step.StartedAt, EndedAt: step.EndedAt})
	}
	return out
}

func issuesForPrefix(issues []WebsiteIssue, prefix string) []WebsiteIssue {
	out := []WebsiteIssue{}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Code, prefix) || strings.Contains(issue.Code, "dns") {
			out = append(out, issue)
		}
	}
	return out
}

func (s *Service) CheckSetupDNS(panelURL string, dnsMode string) SetupDNSCheckResponse {
	panelURL = settings.NormalizeHost(panelURL)
	setup, _ := s.settings.Load()
	if panelURL == "" {
		panelURL = setup.PanelURL
	}
	if dnsMode != "managed" && dnsMode != "external" {
		dnsMode = setup.DNSMode
	}
	if dnsMode == "" {
		dnsMode = "external"
	}
	root := settings.RootDomain(panelURL)
	serverIP := firstIPv4()
	checkedAt := time.Now().UTC()
	ns1 := "ns1." + root
	ns2 := "ns2." + root
	if setup.Nameserver1 != "" && settings.RootDomain(setup.Nameserver1) == root {
		ns1 = setup.Nameserver1
	}
	if setup.Nameserver2 != "" && settings.RootDomain(setup.Nameserver2) == root {
		ns2 = setup.Nameserver2
	}

	res := SetupDNSCheckResponse{PanelURL: panelURL, RootDomain: root, ServerIP: serverIP, DNSMode: dnsMode, Status: "waiting", Title: "Enter panel URL", Detail: "Panel DNS will be checked after a hostname is entered.", PanelResolved: []string{}, Nameservers: []string{}, RequiredRecords: []DelegationRecord{}, CheckedAt: checkedAt}
	if panelURL == "" || root == "" {
		return res
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	panelResolved, _ := net.DefaultResolver.LookupHost(ctx, panelURL)
	nsRecords, _ := net.DefaultResolver.LookupNS(ctx, root)
	ns1Resolved, _ := net.DefaultResolver.LookupHost(ctx, ns1)
	ns2Resolved, _ := net.DefaultResolver.LookupHost(ctx, ns2)
	if panelResolved == nil {
		panelResolved = []string{}
	}
	if ns1Resolved == nil {
		ns1Resolved = []string{}
	}
	if ns2Resolved == nil {
		ns2Resolved = []string{}
	}
	res.PanelResolved = panelResolved
	for _, ns := range nsRecords {
		res.Nameservers = append(res.Nameservers, strings.TrimSuffix(strings.ToLower(ns.Host), "."))
	}
	res.PanelRecordOK = containsIP(panelResolved, serverIP)
	res.NameserverAOK = containsIP(ns1Resolved, serverIP) && containsIP(ns2Resolved, serverIP)
	res.DelegationOK = containsHost(res.Nameservers, ns1) && containsHost(res.Nameservers, ns2)

	if dnsMode == "managed" {
		res.RequiredRecords = []DelegationRecord{
			{Type: "GLUE", Name: ns1, Value: serverIP, Where: "Registrar child nameserver", Status: statusFor(res.NameserverAOK), Detail: "Host record for nameserver 1."},
			{Type: "GLUE", Name: ns2, Value: serverIP, Where: "Registrar child nameserver", Status: statusFor(res.NameserverAOK), Detail: "Host record for nameserver 2."},
			{Type: "NS", Name: root, Value: ns1, Where: "Registrar nameserver", Status: statusFor(res.DelegationOK), Detail: "Delegate root domain to Cloudrifts DNS."},
			{Type: "NS", Name: root, Value: ns2, Where: "Registrar nameserver", Status: statusFor(res.DelegationOK), Detail: "Delegate root domain to Cloudrifts DNS."},
		}
		if res.PanelRecordOK && res.DelegationOK && res.NameserverAOK {
			res.Status, res.Title, res.Detail = "verified", "DNS verified", "Nameservers and panel hostname resolve to this server."
		} else if res.DelegationOK || res.NameserverAOK || res.PanelRecordOK {
			res.Status, res.Title, res.Detail = "partial", "DNS partially ready", "Some records are live. Finish the remaining registrar records, then recheck."
		} else {
			res.Status, res.Title, res.Detail = "action_required", "DNS not delegated yet", "Create ns1/ns2 child nameservers and delegate the domain to them."
		}
		return res
	}

	res.RequiredRecords = []DelegationRecord{
		{Type: "A", Name: panelURL, Value: serverIP, Where: "Your DNS provider", Status: statusFor(res.PanelRecordOK), Detail: "Required for panel SSL."},
		{Type: "A", Name: "webmail." + root, Value: serverIP, Where: "Your DNS provider", Status: "optional", Detail: "Needed only when mail role is installed later."},
		{Type: "A", Name: "phpmyadmin." + root, Value: serverIP, Where: "Your DNS provider", Status: "optional", Detail: "Service URL reserved by Cloudrifts."},
	}
	if res.PanelRecordOK {
		res.Status, res.Title, res.Detail = "verified", "Panel DNS verified", panelURL+" resolves to this server."
	} else if len(panelResolved) > 0 {
		res.Status, res.Title, res.Detail = "action_required", "Panel points elsewhere", panelURL+" resolves to "+strings.Join(panelResolved, ", ")+"."
	} else {
		res.Status, res.Title, res.Detail = "action_required", "Panel DNS missing", "Create the panel A record, then recheck."
	}
	return res
}

func containsIP(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func containsHost(values []string, expected string) bool {
	expected = strings.TrimSuffix(strings.ToLower(settings.NormalizeHost(expected)), ".")
	for _, value := range values {
		if strings.TrimSuffix(strings.ToLower(value), ".") == expected {
			return true
		}
	}
	return false
}

func statusFor(ok bool) string {
	if ok {
		return "verified"
	}
	return "required"
}
