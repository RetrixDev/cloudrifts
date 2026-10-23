package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/cloudrifts/panel/internal/config"
	"github.com/cloudrifts/panel/internal/provisioning"
	"github.com/cloudrifts/panel/internal/settings"
)

var domainPattern = regexp.MustCompile(`^(?i)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

type Service struct {
	store    *Store
	settings *settings.Store
	cfg      config.Config
}

type dnsPreflight struct {
	OK       bool
	Status   string
	Resolved []string
	Required DNSRecord
	Issue    *WebsiteIssue
}

func NewService(cfg config.Config, setup *settings.Store) *Service {
	store := NewStore(cfg.DataDir)
	return &Service{store: &store, settings: setup, cfg: cfg}
}

func (s *Service) Overview() (Overview, error) {
	st, err := s.store.Load()
	if err != nil {
		return Overview{}, err
	}
	setup, _ := s.settings.Load()
	sortNewest(&st)
	return Overview{
		Server:     serverSummary(setup),
		Websites:   st.Websites,
		Jobs:       capJobs(st.Jobs),
		Activity:   capActivity(st.Activity),
		Roles:      rolesForSetup(st.Roles, setup),
		SetupSteps: SetupSteps(setup),
	}, nil
}

func (s *Service) Websites() ([]Website, error) {
	st, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	sortNewest(&st)
	return st.Websites, nil
}

func (s *Service) Website(id string) (Website, bool, error) {
	st, err := s.store.Load()
	if err != nil {
		return Website{}, false, err
	}
	for _, site := range st.Websites {
		if site.ID == id {
			return site, true, nil
		}
	}
	return Website{}, false, nil
}

func (s *Service) Jobs() ([]Job, error) {
	st, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	sortNewest(&st)
	return capJobs(st.Jobs), nil
}

func (s *Service) Job(id string) (Job, bool, error) {
	st, err := s.store.Load()
	if err != nil {
		return Job{}, false, err
	}
	for _, job := range st.Jobs {
		if job.ID == id {
			return job, true, nil
		}
	}
	return Job{}, false, nil
}

func (s *Service) Webservers() ([]WebserverOption, error) {
	setup, err := s.settings.Load()
	if err != nil {
		return nil, err
	}
	current := strings.ToLower(strings.TrimSpace(setup.Webserver))
	if current == "" {
		current = "nginx"
	}
	options := []WebserverOption{
		{ID: "nginx", Name: "Nginx", Default: true, Description: "Default Cloudrifts webserver with safe vhost reloads and Let's Encrypt webroot support"},
		{ID: "apache", Name: "Apache", Description: "Apache HTTPD using PHP-FPM through proxy_fcgi"},
		{ID: "openlitespeed", Name: "OpenLiteSpeed", Description: "OpenLiteSpeed adapter for servers with /usr/local/lsws installed"},
		{ID: "litespeed", Name: "LiteSpeed", Description: "LiteSpeed Enterprise adapter for servers with /usr/local/lsws installed"},
	}
	for i := range options {
		options[i].Current = options[i].ID == current
		options[i].Installed = webserverInstalled(options[i].ID)
		if options[i].Current && !options[i].Installed {
			options[i].Status = "needs_install"
		} else if options[i].Current {
			options[i].Status = "active"
		} else if options[i].Installed {
			options[i].Status = "available"
		} else {
			options[i].Status = "not_installed"
		}
	}
	return options, nil
}

func webserverInstalled(id string) bool {
	switch id {
	case "nginx":
		_, err := exec.LookPath("nginx")
		return err == nil
	case "apache":
		_, err := exec.LookPath("apache2ctl")
		return err == nil
	case "openlitespeed", "litespeed":
		_, err := os.Stat("/usr/local/lsws/bin/lswsctrl")
		return err == nil
	default:
		return false
	}
}

func (s *Service) Roles() ([]ServerRole, error) {
	st, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	setup, _ := s.settings.Load()
	return rolesForSetup(st.Roles, setup), nil
}

func (s *Service) Activity() ([]ActivityEvent, error) {
	st, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	sortNewest(&st)
	return capActivity(st.Activity), nil
}

func (s *Service) CreateWebsite(req CreateWebsiteRequest) (CreateWebsiteResponse, error) {
	domain := normalizeDomain(req.Domain)
	if !domainPattern.MatchString(domain) {
		return CreateWebsiteResponse{}, errors.New("enter a valid domain such as example.com")
	}
	app := normalizeApp(req.App)
	if app == "" {
		return CreateWebsiteResponse{}, errors.New("app must be wordpress, woocommerce, or php")
	}
	phpVersion := strings.TrimSpace(req.PHPVersion)
	if phpVersion == "" {
		phpVersion = "8.3"
	}

	var out CreateWebsiteResponse
	_, err := s.store.Save(func(st *state) error {
		for _, site := range st.Websites {
			if strings.EqualFold(site.Domain, domain) {
				return errors.New("this domain is already in your workspace")
			}
		}
		now := time.Now().UTC()
		id := "site-" + token(5)
		systemUser := systemUserFor(id, domain)
		homeDir := "/home/" + systemUser
		databaseName, databaseUser := databaseIdentifiers(id)
		databasePassword := token(14)
		appAdminUser := "cloudrifts_admin"
		appAdminPassword := token(12)
		appAdminEmail := "admin@" + domain
		databaseStatus := "not_required"
		appStatus := "plain_php"
		if app != AppPHP {
			databaseStatus = "queued"
			appStatus = "queued"
		}
		website := Website{
			ID:               id,
			Domain:           domain,
			App:              app,
			Kind:             kindFor(app),
			SystemUser:       systemUser,
			HomeDir:          homeDir,
			DocumentRoot:     homeDir + "/public_html",
			PHPVersion:       phpVersion,
			RuntimeStatus:    "queued",
			WebserverStatus:  "queued",
			DatabaseName:     databaseName,
			DatabaseUser:     databaseUser,
			DatabasePassword: databasePassword,
			DatabaseStatus:   databaseStatus,
			AppStatus:        appStatus,
			AppAdminUser:     appAdminUser,
			AppAdminEmail:    appAdminEmail,
			AppAdminPassword: appAdminPassword,
			StorageBytes:     0,
			SSL:              SSLState{Provider: "lets_encrypt", Status: "checking_dns"},
			Status:           WebsiteProvisioning,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		website = applySSLPreflight(website, firstIPv4(), now)
		website.Status = WebsiteProvisioning
		website.RuntimeStatus = "queued"
		website.WebserverStatus = "queued"
		if website.App != AppPHP {
			website.DatabaseStatus = "queued"
			website.AppStatus = "queued"
		}
		job := queuedCreateWebsiteJob(website, now)
		website.LastJobID = job.ID
		st.Websites = append([]Website{website}, st.Websites...)
		st.Jobs = append([]Job{job}, st.Jobs...)
		st.Activity = append([]ActivityEvent{{
			ID:        "act-" + token(5),
			Icon:      "globe",
			Title:     "Website provisioning queued",
			Detail:    website.Domain,
			Tone:      "violet",
			CreatedAt: now,
		}}, st.Activity...)
		out = CreateWebsiteResponse{Website: website, Job: job}
		return nil
	})
	if err != nil {
		return out, err
	}
	s.dispatchCreateWebsiteJob(out.Website.ID, out.Job.ID)
	s.QueueManagedDNSForWebsite(out.Website)
	return out, nil
}

func (s *Service) RetrySSL(id string) (RetrySSLResponse, bool, error) {
	var out RetrySSLResponse
	found := false
	_, err := s.store.Save(func(st *state) error {
		for i := range st.Websites {
			if st.Websites[i].ID != id {
				continue
			}
			found = true
			now := time.Now().UTC()
			st.Websites[i].SSL.RetryCount++
			st.Websites[i] = applySSLPreflight(st.Websites[i], firstIPv4(), now)
			st.Websites[i].Status = WebsiteProvisioning
			st.Websites[i].RuntimeStatus = "running"
			st.Websites[i].WebserverStatus = "running"
			job := queuedSSLRetryJob(st.Websites[i], now)
			st.Websites[i].LastJobID = job.ID
			st.Websites[i].UpdatedAt = job.UpdatedAt
			st.Jobs = append([]Job{job}, st.Jobs...)
			st.Activity = append([]ActivityEvent{{
				ID:        "act-" + token(5),
				Icon:      "shield",
				Title:     "SSL retry queued",
				Detail:    st.Websites[i].Domain,
				Tone:      "violet",
				CreatedAt: job.UpdatedAt,
			}}, st.Activity...)
			out = RetrySSLResponse{Website: st.Websites[i], Job: job}
			return nil
		}
		return nil
	})
	if err == nil && found {
		s.dispatchCreateWebsiteJob(out.Website.ID, out.Job.ID)
	}
	return out, found, err
}

func (s *Service) dispatchCreateWebsiteJob(siteID string, jobID string) {
	if os.Getenv("CLOUDRIFTS_SYNC_JOBS") == "1" {
		s.runCreateWebsiteJob(siteID, jobID)
		return
	}
	go s.runCreateWebsiteJob(siteID, jobID)
}

func (s *Service) runCreateWebsiteJob(siteID string, jobID string) {
	s.markJobRunning(siteID, jobID)
	site, ok, err := s.Website(siteID)
	if err != nil || !ok {
		s.failJob(jobID, "website_missing", "Website disappeared before provisioning", "The website record was not found when the worker started.", "Retry creation or restore panel-state.json from backup.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, err := provisioning.RunCreateWebsite(ctx, provisioning.RunOptions{
		Mode:      provisioning.Mode(s.cfg.ExecutionMode),
		AgentPath: s.cfg.AgentPath,
		SudoPath:  s.cfg.SudoPath,
		DataDir:   s.cfg.DataDir,
	}, s.provisionSpec(jobID, site))
	if err != nil {
		result = provisioning.Result{Status: provisioning.ResultFailed, Issues: []provisioning.Issue{{Code: "provisioner_failed", Severity: "critical", Title: "Provisioner crashed", Detail: err.Error(), Fix: "Restart cloudriftsd and retry website creation.", Retryable: true}}, Logs: []string{time.Now().UTC().Format(time.RFC3339) + " provisioner crashed: " + err.Error()}}
	}
	s.applyProvisionResult(siteID, jobID, result)
}

func (s *Service) markJobRunning(siteID string, jobID string) {
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
		if site := findWebsite(st, siteID); site != nil {
			site.Status = WebsiteProvisioning
			site.RuntimeStatus = "running"
			site.WebserverStatus = "running"
			if site.App != AppPHP {
				site.DatabaseStatus = "running"
				site.AppStatus = "running"
			}
			site.UpdatedAt = now
		}
		return nil
	})
}

func (s *Service) failJob(jobID, code, title, detail, fix string) {
	now := time.Now().UTC()
	issue := WebsiteIssue{Code: code, Severity: "critical", Title: title, Detail: detail, Fix: fix, Retryable: true}
	_, _ = s.store.Save(func(st *state) error {
		if j := findJob(st, jobID); j != nil {
			j.Status = JobFailed
			j.Issues = []WebsiteIssue{issue}
			j.Logs = append(j.Logs, now.Format(time.RFC3339)+" "+detail)
			j.UpdatedAt = now
		}
		return nil
	})
}

func (s *Service) applyProvisionResult(siteID string, jobID string, result provisioning.Result) {
	now := time.Now().UTC()
	_, _ = s.store.Save(func(st *state) error {
		job := findJob(st, jobID)
		site := findWebsite(st, siteID)
		if job != nil {
			job.Steps = panelSteps(result.Steps)
			if len(job.Steps) == 0 {
				job.Steps = []JobStep{{ID: "provision", Title: "Provision website", Detail: "Provisioner did not return step details", Status: StepFailed, StartedAt: &now, EndedAt: &now}}
			}
			job.Issues = panelIssues(result.Issues)
			job.Logs = append(job.Logs, result.Logs...)
			job.UpdatedAt = now
			if result.Status == provisioning.ResultFailed {
				job.Status = JobFailed
			} else {
				job.Status = JobDone
			}
		}
		if site != nil {
			site.Issues = mergeProvisionIssues(site.Issues, panelIssues(result.Issues))
			site.NextActions = nextActions(*site)
			site.UpdatedAt = now
			site.RuntimeStatus = "running"
			site.WebserverStatus = "running"
			if site.App == AppPHP {
				site.DatabaseStatus = "not_required"
				site.AppStatus = "plain_php"
			} else {
				if result.DatabaseCreated {
					site.DatabaseStatus = "ready"
				}
				if result.AppInstalled {
					site.AppStatus = "installed"
				}
			}
			if result.Status == provisioning.ResultFailed {
				site.Status = WebsiteFailed
				site.Healthy = false
				site.RuntimeStatus = "failed"
				site.WebserverStatus = "failed"
				if site.App != AppPHP {
					if site.DatabaseStatus != "ready" {
						site.DatabaseStatus = "failed"
					}
					if site.AppStatus != "installed" {
						site.AppStatus = "failed"
					}
				}
				site.SSLStatus = "not_ready"
				site.SSL.Status = "not_ready"
				site.SSL.CanRetry = true
			} else if result.SSLIssued {
				site.Status = WebsiteActive
				site.Healthy = true
				site.DNSStatus = "ok"
				site.SSLStatus = "active"
				site.SSL.Status = "active"
				site.SSL.Reason = "Let's Encrypt certificate is installed at " + result.Certificate
				site.SSL.CanRetry = false
				site.Issues = withoutIssuePrefix(withoutIssuePrefix(site.Issues, "dns_"), "ssl_")
			} else if hasBlockingIssue(site.Issues) {
				site.Status = WebsiteAttention
				site.Healthy = false
				site.SSL.CanRetry = true
				if site.SSLStatus == "" || site.SSLStatus == "checking_dns" || site.SSLStatus == "ready_for_issue" {
					site.SSLStatus = "needs_attention"
					site.SSL.Status = "needs_attention"
				}
			} else {
				site.Status = WebsiteActive
				site.Healthy = true
				site.SSL.CanRetry = site.SSLStatus != "active"
			}
			site.NextActions = nextActions(*site)
			st.Activity = append([]ActivityEvent{{
				ID:        "act-" + token(5),
				Icon:      activityIconForWebsite(*site),
				Title:     activityTitleForProvision(*site, result),
				Detail:    activityDetailForWebsite(*site),
				Tone:      activityToneForWebsite(*site),
				CreatedAt: now,
			}}, st.Activity...)
		}
		return nil
	})
}

func (s *Service) provisionSpec(jobID string, site Website) provisioning.WebsiteSpec {
	recordName := site.Domain
	recordValue := firstIPv4()
	if len(site.RequiredRecords) > 0 {
		recordName = site.RequiredRecords[0].Name
		recordValue = site.RequiredRecords[0].Value
	}
	setup, _ := s.settings.Load()
	return provisioning.WebsiteSpec{
		JobID:            jobID,
		SiteID:           site.ID,
		Domain:           site.Domain,
		App:              string(site.App),
		PHPVersion:       site.PHPVersion,
		Webserver:        setup.Webserver,
		SystemUser:       site.SystemUser,
		SystemGroup:      "cloudrifts-sites",
		HomeDir:          site.HomeDir,
		DocumentRoot:     site.DocumentRoot,
		LogDir:           site.HomeDir + "/logs",
		PublicIP:         firstIPv4(),
		DNSOK:            site.DNSStatus == "ok",
		RequiredAName:    recordName,
		RequiredAValue:   recordValue,
		DatabaseName:     site.DatabaseName,
		DatabaseUser:     site.DatabaseUser,
		DatabasePassword: site.DatabasePassword,
		AppAdminUser:     site.AppAdminUser,
		AppAdminPassword: site.AppAdminPassword,
		AppAdminEmail:    site.AppAdminEmail,
		AppTitle:         site.Domain,
	}
}

func (s *Service) Diagnostics(id string) (WebsiteDiagnostics, bool, error) {
	site, ok, err := s.Website(id)
	if err != nil || !ok {
		return WebsiteDiagnostics{}, ok, err
	}
	checks := []DiagnosticCheck{
		{ID: "domain", Title: "Domain format", Status: "passed", Detail: "Domain is valid for website hosting."},
		{ID: "document_root", Title: "Document root", Status: diagnosticStatus(site.DocumentRoot != "" && site.WebserverStatus != "failed"), Detail: site.DocumentRoot},
		{ID: "php", Title: "PHP runtime", Status: diagnosticStatus(site.RuntimeStatus == "running" || site.Status == WebsiteActive || site.Status == WebsiteAttention), Detail: "PHP " + site.PHPVersion + " pool status: " + emptyAs(site.RuntimeStatus, "unknown")},
		{ID: "webserver", Title: "Webserver", Status: diagnosticStatus(site.WebserverStatus == "running" || site.Status == WebsiteActive || site.Status == WebsiteAttention), Detail: "Webserver vhost status: " + emptyAs(site.WebserverStatus, "unknown")},
	}
	if site.DNSStatus == "ok" {
		checks = append(checks, DiagnosticCheck{ID: "dns", Title: "DNS preflight", Status: "passed", Detail: "Domain resolves to the Cloudrifts server.", Evidence: recordsEvidence(site.RequiredRecords)})
	} else {
		fix := "Create or update the required A record, wait for DNS propagation, then click Retry SSL."
		checks = append(checks, DiagnosticCheck{ID: "dns", Title: "DNS preflight", Status: "action_required", Detail: issueDetail(site, "dns_a_record"), Fix: fix, Evidence: recordsEvidence(site.RequiredRecords)})
	}
	sslStatus := "waiting"
	sslDetail := "Cloudrifts will request a Let's Encrypt certificate after DNS is correct."
	if site.SSLStatus == "active" {
		sslStatus = "passed"
		sslDetail = "SSL is active."
	} else if site.SSLStatus == "ready_for_issue" {
		sslStatus = "passed"
		sslDetail = "DNS is ready; certificate issue can run now."
	} else if site.SSLStatus == "needs_attention" || site.SSLStatus == "dns_check_required" {
		sslStatus = "action_required"
	}
	checks = append(checks, DiagnosticCheck{ID: "ssl", Title: "SSL readiness", Status: sslStatus, Detail: sslDetail, Fix: site.SSL.Reason})
	return WebsiteDiagnostics{Website: site, Checks: checks, RequiredRecord: site.RequiredRecords, CanRetrySSL: site.SSL.CanRetry}, true, nil
}

func SetupSteps(setup settings.SetupSettings) []SetupStep {
	return []SetupStep{
		{ID: "panel_url", Label: "Panel URL", Done: setup.PanelURL != "", Action: "Set the control panel hostname"},
		{ID: "service_urls", Label: "Service URLs", Done: setup.WebmailURL != "" && setup.PhpMyAdminURL != "", Action: "Confirm webmail and phpMyAdmin URLs"},
		{ID: "dns", Label: "DNS", Done: setup.DNSMode != "" && (setup.DNSMode == "external" || (setup.Nameserver1 != "" && setup.Nameserver2 != "")), Action: "Choose managed DNS or external DNS"},
		{ID: "ssl", Label: "Panel SSL", Done: setup.PanelSSLStatus == "active", Action: "Issue trusted SSL for the panel URL"},
		{ID: "roles", Label: "Server roles", Done: setup.Webserver != "" && setup.Database != "", Action: "Select webserver and database role"},
		{ID: "backup", Label: "Backups", Done: strings.HasPrefix(setup.BackupPath, "/"), Action: "Set local backup path"},
	}
}

func rolesForSetup(roles []ServerRole, setup settings.SetupSettings) []ServerRole {
	out := make([]ServerRole, len(roles))
	copy(out, roles)
	name := activeWebserverName(setup.Webserver)
	for i := range out {
		if out[i].ID == "web" || out[i].Kind == "webserver" {
			out[i].Name = name
			out[i].Version = "managed"
			out[i].Status = "running"
			out[i].Description = "Active HTTP engine for website vhosts"
		}
		if out[i].ID == "dns" || out[i].Kind == "dns" {
			if setup.DNSMode == "external" {
				out[i].Name = "External DNS"
				out[i].Version = "external"
				out[i].Status = "external"
				out[i].Description = "DNS records are managed outside Cloudrifts"
			} else {
				out[i].Name = "PowerDNS"
				out[i].Version = "managed"
				if out[i].Status == "" || out[i].Status == "external" {
					out[i].Status = "not_configured"
				}
				out[i].Description = "Authoritative DNS role for managed website zones"
			}
		}
		if out[i].ID == "mail" || out[i].Kind == "mail" {
			out[i].Name = "Email"
			if !setup.EmailEnabled {
				out[i].Version = "disabled"
				out[i].Status = "disabled"
				out[i].Description = "Mail role is disabled until configured"
			} else {
				out[i].Version = "managed"
				if out[i].Status == "" || out[i].Status == "disabled" {
					out[i].Status = "not_configured"
				}
				out[i].Description = "Postfix and Dovecot role for SMTP and IMAP"
			}
		}
	}
	return out
}

func activeWebserverName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apache", "apache2":
		return "Apache"
	case "openlitespeed", "ols":
		return "OpenLiteSpeed"
	case "litespeed", "lsws":
		return "LiteSpeed"
	default:
		return "Nginx"
	}
}

func serverSummary(setup settings.SetupSettings) ServerSummary {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "cloudrifts-local"
	}
	return ServerSummary{
		Name:       "Master server",
		Hostname:   hostname,
		OS:         readOSName(),
		Region:     "Local server",
		IPv4:       firstIPv4(),
		Mode:       "single-server",
		Webserver:  setup.Webserver,
		Database:   setup.Database,
		DNSMode:    setup.DNSMode,
		BackupPath: setup.BackupPath,
	}
}

func applySSLPreflight(site Website, expectedIP string, checkedAt time.Time) Website {
	check := runDNSPreflight(site.Domain, expectedIP)
	site.DNSStatus = check.Status
	site.RequiredRecords = []DNSRecord{check.Required}
	site.Issues = withoutIssuePrefix(site.Issues, "dns_")
	site.Issues = withoutIssuePrefix(site.Issues, "ssl_blocked_by_dns")
	site.SSL.Provider = "lets_encrypt"
	site.SSL.LastCheckedAt = &checkedAt
	if check.OK {
		site.Healthy = true
		site.SSLStatus = "ready_for_issue"
		site.SSL.Status = "ready_for_issue"
		site.SSL.Reason = "DNS is correct. Certificate worker can issue the certificate."
		site.SSL.CanRetry = false
	} else {
		site.Healthy = false
		site.SSLStatus = "dns_check_required"
		site.SSL.Status = "blocked_by_dns"
		site.SSL.Reason = check.Issue.Detail
		site.SSL.CanRetry = true
		site.Issues = append(site.Issues, *check.Issue)
	}
	site.NextActions = nextActions(site)
	return hydrateStoredWebsite(site)
}

func runDNSPreflight(domain string, expectedIP string) dnsPreflight {
	required := DNSRecord{Type: "A", Name: domain, Value: expectedIP, Status: "required", Reason: "Point this record to the Cloudrifts server IP"}
	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()
	resolved, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil || len(resolved) == 0 {
		issue := WebsiteIssue{Code: "dns_a_record_missing", Severity: "warning", Title: "DNS is not pointing yet", Detail: fmt.Sprintf("%s does not currently resolve to this server. SSL cannot be issued until DNS points to %s.", domain, expectedIP), Fix: fmt.Sprintf("Create an A record: %s → %s. Then click Retry SSL.", domain, expectedIP), Retryable: true}
		required.Status = "missing"
		required.Reason = "No matching A record was found during preflight"
		return dnsPreflight{OK: false, Status: "missing", Required: required, Issue: &issue}
	}
	for _, value := range resolved {
		if value == expectedIP {
			required.Status = "ok"
			required.Reason = "Domain resolves to the Cloudrifts server"
			return dnsPreflight{OK: true, Status: "ok", Resolved: resolved, Required: required}
		}
	}
	issue := WebsiteIssue{Code: "dns_a_record_mismatch", Severity: "warning", Title: "DNS points somewhere else", Detail: fmt.Sprintf("%s resolves to %s, but this Cloudrifts server is %s.", domain, strings.Join(resolved, ", "), expectedIP), Fix: fmt.Sprintf("Update the A record to %s, wait for propagation, then click Retry SSL.", expectedIP), Retryable: true}
	required.Status = "mismatch"
	required.Reason = "Resolved IP does not match this Cloudrifts server"
	return dnsPreflight{OK: false, Status: "mismatch", Resolved: resolved, Required: required, Issue: &issue}
}

func queuedCreateWebsiteJob(site Website, started time.Time) Job {
	return queuedWebsiteJob("website.create", "Create website", site, []JobStep{
		{ID: "validate", Title: "Validate server and request", Detail: "Check Ubuntu, root helper, Nginx, PHP-FPM, system account names, and document paths", Issues: site.Issues},
		{ID: "account", Title: "Create isolated system account", Detail: "Create Linux user, group, home, document root, private temp directory, logs, and starter index"},
		{ID: "database", Title: "Prepare database", Detail: databaseStep(site.App)},
		{ID: "app", Title: "Install application", Detail: appStep(site.App)},
		{ID: "runtime", Title: "Prepare PHP-FPM pool", Detail: "Create a per-site PHP-FPM pool and validate PHP-FPM configuration"},
		{ID: "vhost", Title: "Generate webserver virtual host", Detail: "Write HTTP vhost, ACME challenge route, PHP mapping, logs, and validate active webserver config"},
		{ID: "reload", Title: "Reload services", Detail: "Reload PHP-FPM and Nginx only after both configs validate"},
		{ID: "ssl", Title: "Issue SSL certificate", Detail: sslStepDetail(site), Issues: site.Issues},
	}, started)
}

func queuedSSLRetryJob(site Website, started time.Time) Job {
	return queuedWebsiteJob("website.ssl.retry", "Retry SSL", site, []JobStep{
		{ID: "validate", Title: "Validate website and DNS", Detail: dnsStepDetail(site), Issues: site.Issues},
		{ID: "vhost", Title: "Verify HTTP challenge route", Detail: "Ensure the website serves /.well-known/acme-challenge/ from its document root"},
		{ID: "reload", Title: "Reload services", Detail: "Reload PHP-FPM and Nginx if the worker repaired any config"},
		{ID: "ssl", Title: "Issue SSL certificate", Detail: sslStepDetail(site), Issues: site.Issues},
	}, started)
}

func queuedWebsiteJob(jobType string, title string, site Website, steps []JobStep, started time.Time) Job {
	for i := range steps {
		steps[i].Status = StepPending
	}
	return Job{ID: "job-" + token(6), Type: jobType, Title: title, TargetID: site.ID, TargetName: site.Domain, Status: JobQueued, Steps: steps, Issues: issuesOrEmpty(site.Issues), Logs: []string{started.Format(time.RFC3339) + " job queued for " + site.Domain}, CreatedAt: started, UpdatedAt: started}
}

func panelSteps(in []provisioning.StepResult) []JobStep {
	out := make([]JobStep, 0, len(in))
	for _, step := range in {
		out = append(out, JobStep{ID: step.ID, Title: step.Title, Detail: step.Detail, Status: JobStepStatus(step.Status), Issues: panelIssues(step.Issues), StartedAt: step.StartedAt, EndedAt: step.EndedAt})
	}
	return out
}

func panelIssues(in []provisioning.Issue) []WebsiteIssue {
	out := make([]WebsiteIssue, 0, len(in))
	for _, issue := range in {
		out = append(out, WebsiteIssue{Code: issue.Code, Severity: issue.Severity, Title: issue.Title, Detail: issue.Detail, Fix: issue.Fix, Retryable: issue.Retryable})
	}
	return out
}

func mergeProvisionIssues(existing []WebsiteIssue, incoming []WebsiteIssue) []WebsiteIssue {
	out := make([]WebsiteIssue, 0, len(existing)+len(incoming))
	seen := map[string]bool{}
	for _, issue := range existing {
		if strings.HasPrefix(issue.Code, "certificate_") || strings.HasPrefix(issue.Code, "certbot_") || strings.HasPrefix(issue.Code, "ssl_") || strings.HasSuffix(issue.Code, "_failed") || strings.HasPrefix(issue.Code, "agent_") {
			continue
		}
		if !seen[issue.Code] {
			out = append(out, issue)
			seen[issue.Code] = true
		}
	}
	for _, issue := range incoming {
		if !seen[issue.Code] {
			out = append(out, issue)
			seen[issue.Code] = true
		}
	}
	if out == nil {
		return []WebsiteIssue{}
	}
	return out
}

func hasBlockingIssue(issues []WebsiteIssue) bool {
	for _, issue := range issues {
		if issue.Severity == "critical" || issue.Severity == "warning" {
			return true
		}
	}
	return false
}

func findJob(st *state, id string) *Job {
	for i := range st.Jobs {
		if st.Jobs[i].ID == id {
			return &st.Jobs[i]
		}
	}
	return nil
}

func findWebsite(st *state, id string) *Website {
	for i := range st.Websites {
		if st.Websites[i].ID == id {
			return &st.Websites[i]
		}
	}
	return nil
}

func databaseStep(app WebsiteApp) string {
	if app == AppPHP {
		return "Skip app database by default; keep database creation available from website settings"
	}
	return "Create MariaDB database, user, password, grants, and store credentials for app installer"
}

func appStep(app WebsiteApp) string {
	switch app {
	case AppWooCommerce:
		return "Install WordPress core, configure wp-config.php, create admin user, and activate WooCommerce"
	case AppWordPress:
		return "Install WordPress core, configure wp-config.php, and create admin user"
	default:
		return "Plain PHP starter page only"
	}
}

func dnsStepDetail(site Website) string {
	if site.DNSStatus == "ok" {
		return "Domain resolves to this Cloudrifts server"
	}
	if len(site.RequiredRecords) > 0 {
		r := site.RequiredRecords[0]
		return fmt.Sprintf("Needs %s record: %s → %s", r.Type, r.Name, r.Value)
	}
	return "DNS check needs attention before SSL can be issued"
}

func sslStepDetail(site Website) string {
	if site.SSLStatus == "ready_for_issue" || site.SSLStatus == "active" {
		return "DNS is ready; certificate issue can proceed"
	}
	return "SSL is blocked until DNS points to this server; no manual log hunting needed"
}

func nextActions(site Website) []WebsiteAction {
	actions := []WebsiteAction{{ID: "open", Label: "Open website", Kind: "link", Enabled: site.Status != WebsiteFailed}, {ID: "files", Label: "Open files", Kind: "drawer", Enabled: site.Status != WebsiteFailed}, {ID: "backup", Label: "Create backup", Kind: "job", Enabled: site.Status == WebsiteActive || site.Status == WebsiteAttention}}
	if site.SSL.CanRetry || site.SSLStatus == "dns_check_required" || site.SSLStatus == "needs_attention" {
		actions = append([]WebsiteAction{{ID: "retry_ssl", Label: "Retry SSL", Kind: "job", Enabled: true}}, actions...)
	}
	return actions
}

func activityIconForWebsite(site Website) string {
	if site.Status == WebsiteFailed {
		return "warning"
	}
	if len(site.Issues) > 0 {
		return "shield"
	}
	return "globe"
}

func activityTitleForProvision(site Website, result provisioning.Result) string {
	if result.Status == provisioning.ResultFailed || site.Status == WebsiteFailed {
		return "Website provisioning failed"
	}
	if len(site.Issues) > 0 {
		return "Website live · SSL needs attention"
	}
	return "Website provisioned"
}

func activityTitleForWebsite(site Website) string {
	if site.Status == WebsiteFailed {
		return "Website provisioning failed"
	}
	if len(site.Issues) > 0 {
		return "Website created · SSL needs DNS"
	}
	return "Website provisioned"
}

func activityDetailForWebsite(site Website) string {
	if len(site.Issues) > 0 {
		return site.Issues[0].Fix
	}
	return site.Domain
}

func activityTitleForSSL(site Website) string {
	if len(site.Issues) > 0 {
		return "SSL retry needs DNS"
	}
	return "SSL retry ready"
}

func activityToneForWebsite(site Website) string {
	if site.Status == WebsiteFailed {
		return "rose"
	}
	if len(site.Issues) > 0 {
		return "amber"
	}
	return "violet"
}

func issueDetail(site Website, prefix string) string {
	for _, issue := range site.Issues {
		if strings.HasPrefix(issue.Code, prefix) {
			return issue.Detail
		}
	}
	return "This check needs attention."
}

func recordsEvidence(records []DNSRecord) string {
	if len(records) == 0 {
		return ""
	}
	r := records[0]
	return fmt.Sprintf("%s %s %s", r.Type, r.Name, r.Value)
}

func withoutIssuePrefix(in []WebsiteIssue, prefix string) []WebsiteIssue {
	out := []WebsiteIssue{}
	for _, issue := range in {
		if !strings.HasPrefix(issue.Code, prefix) {
			out = append(out, issue)
		}
	}
	return out
}

func issuesOrEmpty(issues []WebsiteIssue) []WebsiteIssue {
	if issues == nil {
		return []WebsiteIssue{}
	}
	return issues
}

func diagnosticStatus(ok bool) string {
	if ok {
		return "passed"
	}
	return "action_required"
}

func emptyAs(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeDomain(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.Trim(value, "./")
	if h, _, err := net.SplitHostPort(value); err == nil {
		value = h
	}
	return strings.Trim(value, ".")
}

func normalizeApp(app WebsiteApp) WebsiteApp {
	switch WebsiteApp(strings.ToLower(strings.TrimSpace(string(app)))) {
	case AppWordPress:
		return AppWordPress
	case AppWooCommerce:
		return AppWooCommerce
	case AppPHP:
		return AppPHP
	default:
		return ""
	}
}

func kindFor(app WebsiteApp) string {
	switch app {
	case AppWooCommerce:
		return "store"
	case AppWordPress:
		return "journal"
	default:
		return "travel"
	}
}

func safeSystemName(domain string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, normalizeDomain(domain))
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if len(name) > 32 {
		name = strings.Trim(name[:32], "-")
	}
	if name == "" {
		name = "site-" + token(3)
	}
	return name
}

func databaseIdentifiers(id string) (string, string) {
	name := strings.ReplaceAll(strings.ToLower(id), "-", "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "site_" + token(3)
	}
	if len(name) > 40 {
		name = name[:40]
	}
	return "crdb_" + name, "cru_" + name
}

func systemUserFor(id string, domain string) string {
	raw := id
	if raw == "" {
		raw = safeSystemName(domain)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = strings.ReplaceAll(safeSystemName(domain), "-", "_")
	}
	name = "cr_" + name
	if len(name) > 30 {
		name = name[:30]
	}
	return strings.TrimRight(name, "_")
}

func token(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func capJobs(jobs []Job) []Job {
	if len(jobs) == 0 {
		return []Job{}
	}
	if len(jobs) > 12 {
		return jobs[:12]
	}
	return jobs
}

func capActivity(events []ActivityEvent) []ActivityEvent {
	if len(events) == 0 {
		return []ActivityEvent{}
	}
	if len(events) > 20 {
		return events[:20]
	}
	return events
}

func readOSName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Local development"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return "Linux"
}

var cachedFirstIPv4 string

func firstIPv4() string {
	if cachedFirstIPv4 != "" {
		return cachedFirstIPv4
	}
	for _, candidate := range []string{
		strings.TrimSpace(os.Getenv("CLOUDRIFTS_PUBLIC_IP")),
		ec2PublicIPv4(),
		publicIPv4FromHTTPS(),
		localInterfaceIPv4(),
	} {
		if isIPv4(candidate) {
			cachedFirstIPv4 = candidate
			return cachedFirstIPv4
		}
	}
	cachedFirstIPv4 = "127.0.0.1"
	return cachedFirstIPv4
}

func ec2PublicIPv4() string {
	client := &http.Client{Timeout: time.Second}
	req, err := http.NewRequest(http.MethodPut, "http://169.254.169.254/latest/api/token", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ""
	}
	tokenBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return ""
	}
	req, err = http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/public-ipv4", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err = client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(body))
}

func publicIPv4FromHTTPS() string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(body))
}

func localInterfaceIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip == nil || ip.To4() == nil {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func isIPv4(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	return ip != nil && ip.To4() != nil
}
