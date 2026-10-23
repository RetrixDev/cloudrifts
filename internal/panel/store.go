package panel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	path string
	mu   sync.Mutex
}

type state struct {
	Websites []Website       `json:"websites"`
	Jobs     []Job           `json:"jobs"`
	Backups  []Backup        `json:"backups"`
	DNSZones []DNSZone       `json:"dns_zones"`
	Activity []ActivityEvent `json:"activity"`
	Roles    []ServerRole    `json:"roles"`
	Updated  time.Time       `json:"updated_at"`
}

func NewStore(dataDir string) Store {
	return Store{path: filepath.Join(dataDir, "panel-state.json")}
}

func (s *Store) Load() (state, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) Save(mutator func(*state) error) (state, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.loadLocked()
	if err != nil {
		return state{}, err
	}
	if err := mutator(&st); err != nil {
		return state{}, err
	}
	st.Updated = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return state{}, err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return state{}, err
	}
	if err := os.WriteFile(s.path, b, 0o640); err != nil {
		return state{}, err
	}
	return st, nil
}

func (s *Store) loadLocked() (state, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return withSeedFallback(state{}), nil
		}
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, err
	}
	return withSeedFallback(st), nil
}

func withSeedFallback(st state) state {
	seed := seedState()
	demoData := os.Getenv("CLOUDRIFTS_DEMO_DATA") == "1"
	if len(st.Websites) == 0 && demoData {
		st.Websites = seed.Websites
	}
	for i := range st.Websites {
		st.Websites[i] = hydrateStoredWebsite(st.Websites[i])
	}
	if len(st.Roles) == 0 {
		st.Roles = seed.Roles
	}
	if len(st.Activity) == 0 && demoData {
		st.Activity = seed.Activity
	}
	if st.Websites == nil {
		st.Websites = []Website{}
	}
	if st.Jobs == nil {
		st.Jobs = []Job{}
	}
	if st.Backups == nil {
		st.Backups = []Backup{}
	}
	for i := range st.DNSZones {
		st.DNSZones[i] = hydrateStoredDNSZone(st.DNSZones[i])
	}
	if st.DNSZones == nil {
		st.DNSZones = []DNSZone{}
	}
	if st.Activity == nil {
		st.Activity = []ActivityEvent{}
	}
	return st
}

func seedState() state {
	now := time.Now().UTC()
	websites := []Website{
		seedWebsite("nova", "nova-store.com", AppWooCommerce, "store", 8400000000, true, now.Add(-72*time.Hour)),
		seedWebsite("forma", "forma.studio", AppWordPress, "studio", 4200000000, true, now.Add(-70*time.Hour)),
		seedWebsite("offgrid", "offgrid-journal.com", AppWordPress, "journal", 6100000000, true, now.Add(-48*time.Hour)),
		seedWebsite("atlas", "atlas-travel.com", AppPHP, "travel", 3600000000, false, now.Add(-28*time.Hour)),
		seedWebsite("nova-journal", "journal.nova-store.com", AppWordPress, "journal", 1800000000, true, now.Add(-24*time.Hour)),
		seedWebsite("forma-dev", "staging.forma.studio", AppWordPress, "studio", 700000000, true, now.Add(-12*time.Hour)),
	}
	return state{
		Websites: websites,
		Roles: []ServerRole{
			{ID: "web", Name: "Nginx", Kind: "webserver", Version: "1.26", Status: "running", Description: "Active HTTP engine for website vhosts", UpdatedAt: now},
			{ID: "database", Name: "MariaDB", Kind: "database", Version: "11.4", Status: "running", Description: "Default database engine for WordPress and PHP apps", UpdatedAt: now},
			{ID: "dns", Name: "External DNS", Kind: "dns", Version: "external", Status: "external", Description: "DNS records are managed outside Cloudrifts until managed DNS is enabled", UpdatedAt: now},
			{ID: "mail", Name: "Email", Kind: "mail", Version: "disabled", Status: "disabled", Description: "Mail role is disabled until configured", UpdatedAt: now},
			{ID: "php", Name: "PHP-FPM", Kind: "runtime", Version: "8.3", Status: "running", Description: "Per-site PHP pools and runtime isolation", UpdatedAt: now},
			{ID: "backup", Name: "Backups", Kind: "backup", Version: "local", Status: "ready", Description: "Local snapshot and restore engine", UpdatedAt: now},
		},
		Activity: []ActivityEvent{
			{ID: "act-backup", Icon: "backup", Title: "Backup completed", Detail: "All 6 websites", Tone: "mint", CreatedAt: now.Add(-2 * time.Hour)},
			{ID: "act-ssl", Icon: "shield", Title: "SSL renewed", Detail: "nova-store.com", Tone: "violet", CreatedAt: now.Add(-3 * time.Hour)},
			{ID: "act-site", Icon: "globe", Title: "Website created", Detail: "offgrid-journal.com", Tone: "blue", CreatedAt: now.Add(-24 * time.Hour)},
		},
		Updated: now,
	}
}

func seedWebsite(id string, domain string, app WebsiteApp, kind string, storage int64, healthy bool, created time.Time) Website {
	status := WebsiteActive
	sslStatus := "active"
	dnsStatus := "ok"
	issues := []WebsiteIssue{}
	records := []DNSRecord{}
	if !healthy {
		status = WebsiteAttention
		sslStatus = "dns_check_required"
		dnsStatus = "mismatch"
		issues = append(issues, WebsiteIssue{Code: "dns_a_record_mismatch", Severity: "warning", Title: "DNS record needs attention", Detail: "The website can be created, but SSL needs the domain A record to point to this server.", Fix: "Update the A record, then retry SSL from Cloudrifts.", Retryable: true})
		records = append(records, DNSRecord{Type: "A", Name: domain, Value: "127.0.0.1", Status: "required", Reason: "Point this record to the Cloudrifts server IP"})
	}
	return hydrateStoredWebsite(Website{
		ID:              id,
		Domain:          domain,
		App:             app,
		Kind:            kind,
		DocumentRoot:    "/home/" + id + "/public_html",
		PHPVersion:      "8.3",
		StorageBytes:    storage,
		Healthy:         healthy,
		DNSStatus:       dnsStatus,
		SSLStatus:       sslStatus,
		SSL:             SSLState{Provider: "lets_encrypt", Status: sslStatus, CanRetry: !healthy},
		RequiredRecords: records,
		Issues:          issues,
		Status:          status,
		CreatedAt:       created,
		UpdatedAt:       created,
	})
}

func hydrateStoredWebsite(site Website) Website {
	if site.App == "" {
		site.App = AppPHP
	}
	if site.Kind == "" {
		site.Kind = kindFor(site.App)
	}
	if site.PHPVersion == "" {
		site.PHPVersion = "8.3"
	}
	if site.SystemUser == "" {
		site.SystemUser = systemUserFor(site.ID, site.Domain)
	}
	if site.HomeDir == "" {
		site.HomeDir = "/home/" + site.SystemUser
	}
	if site.DocumentRoot == "" {
		site.DocumentRoot = site.HomeDir + "/public_html"
	}
	if site.RuntimeStatus == "" {
		site.RuntimeStatus = "unknown"
	}
	if site.WebserverStatus == "" {
		site.WebserverStatus = "unknown"
	}
	if site.DatabaseStatus == "" {
		if site.App == AppPHP {
			site.DatabaseStatus = "not_required"
		} else {
			site.DatabaseStatus = "unknown"
		}
	}
	if site.AppStatus == "" {
		if site.App == AppPHP {
			site.AppStatus = "plain_php"
		} else {
			site.AppStatus = "unknown"
		}
	}
	if site.DNSStatus == "" {
		if site.Healthy {
			site.DNSStatus = "ok"
		} else {
			site.DNSStatus = "unknown"
		}
	}
	if site.SSLStatus == "" {
		if site.Healthy {
			site.SSLStatus = "active"
		} else {
			site.SSLStatus = "dns_check_required"
		}
	}
	if site.SSL.Provider == "" {
		site.SSL.Provider = "lets_encrypt"
	}
	if site.SSL.Status == "" {
		site.SSL.Status = site.SSLStatus
	}
	if site.Status == "" {
		if site.Healthy {
			site.Status = WebsiteActive
		} else {
			site.Status = WebsiteAttention
		}
	}
	if site.RequiredRecords == nil {
		site.RequiredRecords = []DNSRecord{}
	}
	if site.Issues == nil {
		site.Issues = []WebsiteIssue{}
	}
	if site.NextActions == nil {
		site.NextActions = nextActions(site)
	}
	return site
}

func hydrateStoredDNSZone(zone DNSZone) DNSZone {
	if zone.Mode == "" {
		zone.Mode = "managed"
	}
	if zone.Status == "" {
		zone.Status = "pending"
	}
	if zone.Records == nil {
		zone.Records = []DNSZoneRecord{}
	}
	if zone.Issues == nil {
		zone.Issues = []WebsiteIssue{}
	}
	for i := range zone.Records {
		if zone.Records[i].TTL <= 0 {
			zone.Records[i].TTL = 300
		}
		if zone.Records[i].Status == "" {
			zone.Records[i].Status = "pending"
		}
	}
	return zone
}

func sortNewest(st *state) {
	sort.SliceStable(st.Websites, func(i, j int) bool { return st.Websites[i].CreatedAt.After(st.Websites[j].CreatedAt) })
	sort.SliceStable(st.Jobs, func(i, j int) bool { return st.Jobs[i].CreatedAt.After(st.Jobs[j].CreatedAt) })
	sort.SliceStable(st.DNSZones, func(i, j int) bool { return st.DNSZones[i].CreatedAt.After(st.DNSZones[j].CreatedAt) })
	sort.SliceStable(st.Activity, func(i, j int) bool { return st.Activity[i].CreatedAt.After(st.Activity[j].CreatedAt) })
}
