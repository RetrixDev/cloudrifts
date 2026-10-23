package panel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
	"github.com/cloudrifts/panel/internal/settings"
)

func (s *Service) DNSZones() (DNSZonesResponse, error) {
	st, err := s.store.Load()
	if err != nil {
		return DNSZonesResponse{}, err
	}
	setup, _ := s.settings.Load()
	sortNewest(&st)
	return DNSZonesResponse{Role: dnsRoleState(st, setup), Zones: st.DNSZones}, nil
}

func (s *Service) SetupDNSRole() (SetupDNSRoleResponse, error) {
	setup, err := s.settings.Load()
	if err != nil {
		return SetupDNSRoleResponse{}, err
	}
	setup = withDNSDefaults(setup)
	setup.DNSMode = "managed"
	if _, err := s.settings.Save(setup); err != nil {
		return SetupDNSRoleResponse{}, err
	}

	var out SetupDNSRoleResponse
	_, err = s.store.Save(func(st *state) error {
		now := time.Now().UTC()
		job := queuedDNSRoleJob(now)
		setStoredRole(st, ServerRole{ID: "dns", Name: "PowerDNS", Kind: "dns", Version: "managed", Status: "installing", Description: "Authoritative DNS role is being configured on this server", UpdatedAt: now})
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "network",
			Title:     "DNS role setup queued",
			Detail:    setup.Nameserver1 + " / " + setup.Nameserver2,
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = SetupDNSRoleResponse{Role: dnsRoleState(*st, setup), Job: job}
		return nil
	})
	if err != nil {
		return out, err
	}
	if osEnvSyncJobs() {
		s.runDNSRoleJob(out.Job.ID)
	} else {
		go s.runDNSRoleJob(out.Job.ID)
	}
	return out, nil
}

func (s *Service) CreateDNSZone(req CreateDNSZoneRequest) (ApplyDNSZoneResponse, error) {
	domain := normalizeDomain(req.Domain)
	if !domainPattern.MatchString(domain) {
		return ApplyDNSZoneResponse{}, errors.New("enter a valid DNS zone such as example.com")
	}
	setup, _ := s.settings.Load()
	setup = withDNSDefaultsForDomain(setup, domain)

	var out ApplyDNSZoneResponse
	_, err := s.store.Save(func(st *state) error {
		now := time.Now().UTC()
		records := normalizeZoneRecords(domain, req.Records)
		if len(records) == 0 {
			records = defaultDNSZoneRecords(domain, firstIPv4(), setup.Nameserver1, setup.Nameserver2, now)
		}
		zone := findDNSZoneByDomain(st, domain)
		if zone == nil {
			st.DNSZones = append([]DNSZone{{
				ID:          "zone-" + token(5),
				Domain:      domain,
				Mode:        "managed",
				Status:      "queued",
				ServerIP:    firstIPv4(),
				Nameserver1: setup.Nameserver1,
				Nameserver2: setup.Nameserver2,
				Records:     records,
				Issues:      []WebsiteIssue{},
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, st.DNSZones...)
			zone = &st.DNSZones[0]
		} else {
			zone.Mode = "managed"
			zone.Status = "queued"
			zone.ServerIP = firstIPv4()
			zone.Nameserver1 = setup.Nameserver1
			zone.Nameserver2 = setup.Nameserver2
			zone.Records = mergeDNSRecords(zone.Records, records)
			zone.Issues = []WebsiteIssue{}
			zone.UpdatedAt = now
		}
		job := queuedDNSZoneJob(*zone, now)
		zone.LastJobID = job.ID
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "network",
			Title:     "DNS zone apply queued",
			Detail:    domain,
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = ApplyDNSZoneResponse{Zone: *zone, Job: job}
		return nil
	})
	if err != nil {
		return out, err
	}
	s.dispatchDNSZoneJob(out.Zone.ID, out.Job.ID)
	return out, nil
}

func (s *Service) ApplyDNSZone(id string) (ApplyDNSZoneResponse, bool, error) {
	var out ApplyDNSZoneResponse
	found := false
	_, err := s.store.Save(func(st *state) error {
		zone := findDNSZone(st, id)
		if zone == nil {
			return nil
		}
		found = true
		now := time.Now().UTC()
		zone.Status = "queued"
		zone.Issues = []WebsiteIssue{}
		zone.UpdatedAt = now
		job := queuedDNSZoneJob(*zone, now)
		zone.LastJobID = job.ID
		st.Jobs = append([]Job{job}, st.Jobs...)
		out = ApplyDNSZoneResponse{Zone: *zone, Job: job}
		return nil
	})
	if err == nil && found {
		s.dispatchDNSZoneJob(out.Zone.ID, out.Job.ID)
	}
	return out, found, err
}

func (s *Service) UpsertDNSRecord(zoneID string, req UpsertDNSRecordRequest) (UpsertDNSRecordResponse, bool, error) {
	recordType := strings.ToUpper(strings.TrimSpace(req.Type))
	name := strings.TrimSpace(strings.ToLower(req.Name))
	value := strings.TrimSpace(req.Value)
	if name == "" {
		name = "@"
	}
	if recordType == "" || value == "" {
		return UpsertDNSRecordResponse{}, true, errors.New("record type and value are required")
	}
	if req.TTL <= 0 {
		req.TTL = 300
	}
	var out UpsertDNSRecordResponse
	found := false
	_, err := s.store.Save(func(st *state) error {
		zone := findDNSZone(st, zoneID)
		if zone == nil {
			return nil
		}
		found = true
		now := time.Now().UTC()
		record := DNSZoneRecord{ID: "record-" + token(5), Type: recordType, Name: name, Value: value, TTL: req.TTL, Status: "queued", CreatedAt: now, UpdatedAt: now}
		for i := range zone.Records {
			if strings.EqualFold(zone.Records[i].Type, record.Type) && strings.EqualFold(zone.Records[i].Name, record.Name) {
				record.ID = zone.Records[i].ID
				record.CreatedAt = zone.Records[i].CreatedAt
				zone.Records[i] = record
				goto queued
			}
		}
		zone.Records = append(zone.Records, record)
	queued:
		zone.Status = "queued"
		zone.Issues = []WebsiteIssue{}
		zone.UpdatedAt = now
		job := queuedDNSZoneJob(*zone, now)
		zone.LastJobID = job.ID
		st.Jobs = append([]Job{job}, st.Jobs...)
		out = UpsertDNSRecordResponse{Zone: *zone, Record: record, Job: job}
		return nil
	})
	if err == nil && found {
		s.dispatchDNSZoneJob(out.Zone.ID, out.Job.ID)
	}
	return out, found, err
}

func (s *Service) QueueManagedDNSForWebsite(site Website) {
	setup, err := s.settings.Load()
	if err != nil || setup.DNSMode != "managed" {
		return
	}
	setup = withDNSDefaultsForDomain(setup, site.Domain)
	now := time.Now().UTC()
	var zoneID, jobID string
	_, err = s.store.Save(func(st *state) error {
		records := defaultDNSZoneRecords(site.Domain, firstIPv4(), setup.Nameserver1, setup.Nameserver2, now)
		zone := findDNSZoneByDomain(st, site.Domain)
		if zone == nil {
			st.DNSZones = append([]DNSZone{{
				ID:          "zone-" + token(5),
				Domain:      site.Domain,
				Mode:        "managed",
				Status:      "queued",
				ServerIP:    firstIPv4(),
				Nameserver1: setup.Nameserver1,
				Nameserver2: setup.Nameserver2,
				Records:     records,
				Issues:      []WebsiteIssue{},
				CreatedAt:   now,
				UpdatedAt:   now,
			}}, st.DNSZones...)
			zone = &st.DNSZones[0]
		} else {
			zone.Mode = "managed"
			zone.Status = "queued"
			zone.ServerIP = firstIPv4()
			zone.Nameserver1 = setup.Nameserver1
			zone.Nameserver2 = setup.Nameserver2
			zone.Records = mergeDNSRecords(zone.Records, records)
			zone.Issues = []WebsiteIssue{}
			zone.UpdatedAt = now
		}
		job := queuedDNSZoneJob(*zone, now)
		zone.LastJobID = job.ID
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "network",
			Title:     "Managed DNS queued",
			Detail:    site.Domain,
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		zoneID = zone.ID
		jobID = job.ID
		return nil
	})
	if err == nil && zoneID != "" && jobID != "" {
		s.dispatchDNSZoneJob(zoneID, jobID)
	}
}

func (s *Service) dispatchDNSZoneJob(zoneID, jobID string) {
	if osEnvSyncJobs() {
		s.runDNSZoneJob(zoneID, jobID)
		return
	}
	go s.runDNSZoneJob(zoneID, jobID)
}

func (s *Service) runDNSRoleJob(jobID string) {
	s.markGenericJobRunning(jobID)
	setup, _ := s.settings.Load()
	setup = withDNSDefaults(setup)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, err := provisioning.RunSetupDNSRole(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, provisioning.DNSRoleSpec{JobID: jobID, ServerIP: firstIPv4(), Nameserver1: setup.Nameserver1, Nameserver2: setup.Nameserver2})
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "dns_role_worker_crashed", Severity: "critical", Title: "DNS role worker crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry DNS setup.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " DNS role worker crashed: " + err.Error()}}
	}
	s.applyDNSRoleResult(jobID, result)
}

func (s *Service) runDNSZoneJob(zoneID, jobID string) {
	s.markGenericJobRunning(jobID)
	st, err := s.store.Load()
	if err != nil {
		s.failJob(jobID, "dns_zone_state_load_failed", "DNS zone apply failed", err.Error(), "Refresh the panel and retry DNS zone apply.")
		return
	}
	zone := findDNSZone(&st, zoneID)
	if zone == nil {
		s.failJob(jobID, "dns_zone_missing", "DNS zone apply failed", "The DNS zone was not found before the worker started.", "Refresh the panel and recreate the zone.")
		return
	}
	spec := provisioning.DNSZoneSpec{JobID: jobID, ZoneID: zone.ID, Domain: zone.Domain, ServerIP: zone.ServerIP, Nameserver1: zone.Nameserver1, Nameserver2: zone.Nameserver2, Records: make([]provisioning.DNSRecordSpec, 0, len(zone.Records))}
	for _, record := range zone.Records {
		spec.Records = append(spec.Records, provisioning.DNSRecordSpec{Name: record.Name, Type: record.Type, Content: record.Value, TTL: record.TTL})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result, err := provisioning.RunApplyDNSZone(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, spec)
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "dns_zone_worker_crashed", Severity: "critical", Title: "DNS zone worker crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry the DNS zone.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " DNS zone worker crashed: " + err.Error()}}
	}
	s.applyDNSZoneResult(zoneID, jobID, result)
}

func (s *Service) applyDNSRoleResult(jobID string, result provisioning.Result) {
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
		title := "DNS role ready"
		tone := "mint"
		description := "PowerDNS authoritative role is ready for managed zones"
		if result.Status == provisioning.ResultFailed {
			status = "failed"
			title = "DNS role setup failed"
			tone = "rose"
			description = "PowerDNS setup needs attention before managed DNS can serve zones"
		}
		setStoredRole(st, ServerRole{ID: "dns", Name: "PowerDNS", Kind: "dns", Version: "managed", Status: status, Description: description, UpdatedAt: now})
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "network",
			Title:     title,
			Detail:    firstIssueDetail(panelIssues(result.Issues), "Managed DNS role"),
			Tone:      tone,
			CreatedAt: now,
		}}, st.Activity...)
		return nil
	})
}

func (s *Service) applyDNSZoneResult(zoneID, jobID string, result provisioning.Result) {
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		zone := findDNSZone(st, zoneID)
		issues := panelIssues(result.Issues)
		if job != nil {
			job.Steps = panelSteps(result.Steps)
			job.Issues = issues
			job.Logs = append(job.Logs, result.Logs...)
			job.Status = JobDone
			if result.Status == provisioning.ResultFailed {
				job.Status = JobFailed
			}
			job.UpdatedAt = now
		}
		if zone != nil {
			zone.Issues = issues
			zone.UpdatedAt = now
			if result.Status == provisioning.ResultFailed {
				zone.Status = "failed"
				for i := range zone.Records {
					zone.Records[i].Status = "failed"
					zone.Records[i].Reason = firstIssueDetail(issues, "PowerDNS apply failed")
					zone.Records[i].UpdatedAt = now
				}
			} else {
				zone.Status = "active"
				zone.LastAppliedAt = &now
				for i := range zone.Records {
					zone.Records[i].Status = "active"
					zone.Records[i].Reason = "Applied to PowerDNS"
					zone.Records[i].UpdatedAt = now
				}
			}
			title := "DNS zone applied"
			tone := "mint"
			if result.Status == provisioning.ResultFailed {
				title = "DNS zone apply failed"
				tone = "rose"
			}
			st.Activity = append([]ActivityEvent{{
				ID:        "act-" + token(5),
				Icon:      "network",
				Title:     title,
				Detail:    zone.Domain,
				Tone:      tone,
				CreatedAt: now,
			}}, st.Activity...)
		}
		return nil
	})
}

func queuedDNSRoleJob(now time.Time) Job {
	return Job{ID: "job-" + token(6), Type: "server.dns.setup", Title: "Setup managed DNS", TargetID: "server:dns", TargetName: "PowerDNS", Status: JobQueued, Steps: []JobStep{
		{ID: "validate", Title: "Validate DNS role request", Detail: "Check Ubuntu, root helper, PowerDNS packages, nameservers and server IP", Status: StepPending},
		{ID: "database", Title: "Prepare PowerDNS database", Detail: "Create the SQLite backend database and schema", Status: StepPending},
		{ID: "config", Title: "Write PowerDNS config", Detail: "Point PowerDNS to the Cloudrifts backend", Status: StepPending},
		{ID: "service", Title: "Start DNS service", Detail: "Enable and restart PowerDNS", Status: StepPending},
		{ID: "firewall", Title: "Open DNS firewall", Detail: "Open TCP/UDP 53 when UFW is active", Status: StepPending},
	}, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " DNS role setup queued"}, CreatedAt: now, UpdatedAt: now}
}

func queuedDNSZoneJob(zone DNSZone, now time.Time) Job {
	return Job{ID: "job-" + token(6), Type: "dns.zone.apply", Title: "Apply DNS zone", TargetID: zone.ID, TargetName: zone.Domain, Status: JobQueued, Steps: []JobStep{
		{ID: "validate", Title: "Validate zone", Detail: "Check domain, records, nameservers and PowerDNS tooling", Status: StepPending},
		{ID: "zone", Title: "Create or update zone", Detail: "Ensure the authoritative zone exists", Status: StepPending},
		{ID: "records", Title: "Apply records", Detail: fmt.Sprintf("Apply %d managed records", len(zone.Records)), Status: StepPending},
		{ID: "reload", Title: "Reload DNS service", Detail: "Rectify the zone and reload PowerDNS", Status: StepPending},
	}, Issues: []WebsiteIssue{}, Logs: []string{now.Format(time.RFC3339) + " DNS zone apply queued for " + zone.Domain}, CreatedAt: now, UpdatedAt: now}
}

func dnsRoleState(st state, setup settings.SetupSettings) DNSRoleState {
	setup = withDNSDefaults(setup)
	status := "external"
	ready := false
	if setup.DNSMode == "managed" {
		status = "not_configured"
	}
	if role := findRole(&st, "dns"); role != nil {
		status = role.Status
		ready = role.Status == "ready" || role.Status == "running"
	}
	if setup.DNSMode == "external" {
		status = "external"
		ready = false
	}
	return DNSRoleState{Mode: setup.DNSMode, Status: status, Installed: powerDNSInstalled(), Ready: ready, ServerIP: firstIPv4(), Nameserver1: setup.Nameserver1, Nameserver2: setup.Nameserver2, UpdatedAt: st.Updated}
}

func powerDNSInstalled() bool {
	for _, name := range []string{"pdns_server", "pdnsutil"} {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}

func withDNSDefaults(setup settings.SetupSettings) settings.SetupSettings {
	if setup.Nameserver1 != "" && setup.Nameserver2 != "" {
		return setup
	}
	root := rootDomain(setup.PanelURL)
	if root == "" {
		root = "cloudrifts.local"
	}
	if setup.Nameserver1 == "" {
		setup.Nameserver1 = "ns1." + root
	}
	if setup.Nameserver2 == "" {
		setup.Nameserver2 = "ns2." + root
	}
	return setup
}

func withDNSDefaultsForDomain(setup settings.SetupSettings, domain string) settings.SetupSettings {
	setup = withDNSDefaults(setup)
	if setup.Nameserver1 == "ns1.cloudrifts.local" && domain != "" {
		root := rootDomain(domain)
		setup.Nameserver1 = "ns1." + root
		setup.Nameserver2 = "ns2." + root
	}
	return setup
}

func defaultDNSZoneRecords(domain, serverIP, ns1, ns2 string, now time.Time) []DNSZoneRecord {
	out := []DNSZoneRecord{
		{ID: "record-" + token(5), Type: "A", Name: "@", Value: serverIP, TTL: 300, Status: "queued", Reason: "Website apex points to this server", CreatedAt: now, UpdatedAt: now},
		{ID: "record-" + token(5), Type: "CNAME", Name: "www", Value: domain, TTL: 300, Status: "queued", Reason: "www follows the apex website", CreatedAt: now, UpdatedAt: now},
	}
	for _, ns := range []string{ns1, ns2} {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(ns)), ".")
		if strings.HasSuffix(host, "."+domain) {
			label := strings.TrimSuffix(host, "."+domain)
			if label != "" {
				out = append(out, DNSZoneRecord{ID: "record-" + token(5), Type: "A", Name: label, Value: serverIP, TTL: 300, Status: "queued", Reason: "Nameserver host points to this DNS server", CreatedAt: now, UpdatedAt: now})
			}
		}
	}
	return out
}

func normalizeZoneRecords(domain string, records []DNSZoneRecord) []DNSZoneRecord {
	now := time.Now().UTC()
	out := make([]DNSZoneRecord, 0, len(records))
	for _, record := range records {
		record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
		record.Name = strings.Trim(strings.ToLower(strings.TrimSpace(record.Name)), ".")
		record.Value = strings.TrimSpace(record.Value)
		if record.Name == "" || record.Name == domain {
			record.Name = "@"
		}
		if record.TTL <= 0 {
			record.TTL = 300
		}
		if record.Type == "" || record.Value == "" {
			continue
		}
		if record.ID == "" {
			record.ID = "record-" + token(5)
		}
		if record.Status == "" {
			record.Status = "queued"
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		record.UpdatedAt = now
		out = append(out, record)
	}
	return out
}

func mergeDNSRecords(existing, incoming []DNSZoneRecord) []DNSZoneRecord {
	out := append([]DNSZoneRecord{}, existing...)
	for _, record := range incoming {
		replaced := false
		for i := range out {
			if strings.EqualFold(out[i].Type, record.Type) && strings.EqualFold(out[i].Name, record.Name) {
				record.ID = out[i].ID
				record.CreatedAt = out[i].CreatedAt
				out[i] = record
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, record)
		}
	}
	return out
}

func findDNSZone(st *state, id string) *DNSZone {
	for i := range st.DNSZones {
		if st.DNSZones[i].ID == id {
			return &st.DNSZones[i]
		}
	}
	return nil
}

func findDNSZoneByDomain(st *state, domain string) *DNSZone {
	for i := range st.DNSZones {
		if strings.EqualFold(st.DNSZones[i].Domain, domain) {
			return &st.DNSZones[i]
		}
	}
	return nil
}

func findRole(st *state, id string) *ServerRole {
	for i := range st.Roles {
		if st.Roles[i].ID == id {
			return &st.Roles[i]
		}
	}
	return nil
}

func setStoredRole(st *state, role ServerRole) {
	for i := range st.Roles {
		if st.Roles[i].ID == role.ID {
			st.Roles[i] = role
			return
		}
	}
	st.Roles = append(st.Roles, role)
}

func firstIssueDetail(issues []WebsiteIssue, fallback string) string {
	if len(issues) == 0 {
		return fallback
	}
	if issues[0].Detail != "" {
		return issues[0].Detail
	}
	return issues[0].Title
}

func rootDomain(host string) string {
	host = normalizeDomain(host)
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func osEnvSyncJobs() bool {
	return os.Getenv("CLOUDRIFTS_SYNC_JOBS") == "1"
}
