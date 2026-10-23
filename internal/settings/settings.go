package settings

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SetupSettings struct {
	PanelURL       string    `json:"panel_url"`
	WebmailURL     string    `json:"webmail_url"`
	PhpMyAdminURL  string    `json:"phpmyadmin_url"`
	DNSMode        string    `json:"dns_mode"`
	Nameserver1    string    `json:"nameserver_1"`
	Nameserver2    string    `json:"nameserver_2"`
	Webserver      string    `json:"webserver"`
	Database       string    `json:"database"`
	EmailEnabled   bool      `json:"email_enabled"`
	BackupPath     string    `json:"backup_path"`
	PanelSSLStatus string    `json:"panel_ssl_status"`
	PanelSSLReason string    `json:"panel_ssl_reason,omitempty"`
	LastSetupJobID string    `json:"last_setup_job_id,omitempty"`
	ManagedDNSZone string    `json:"managed_dns_zone,omitempty"`
	SetupCompleted bool      `json:"setup_completed"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(dataDir string) Store {
	return Store{path: filepath.Join(dataDir, "setup-settings.json")}
}

func (s *Store) Load() (SetupSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return SetupSettings{}, err
	}
	var out SetupSettings
	if err := json.Unmarshal(b, &out); err != nil {
		return SetupSettings{}, err
	}
	return Normalize(out), nil
}

func (s *Store) Save(in SetupSettings) (SetupSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw := in
	in = Normalize(in)
	if strings.TrimSpace(raw.PanelURL) != "" && in.PanelURL == "" {
		return SetupSettings{}, errors.New("panel_url must be a valid hostname")
	}
	if strings.TrimSpace(raw.WebmailURL) != "" && in.WebmailURL == "" {
		return SetupSettings{}, errors.New("webmail_url must be a valid hostname")
	}
	if strings.TrimSpace(raw.PhpMyAdminURL) != "" && in.PhpMyAdminURL == "" {
		return SetupSettings{}, errors.New("phpmyadmin_url must be a valid hostname")
	}
	if strings.TrimSpace(raw.Nameserver1) != "" && in.Nameserver1 == "" {
		return SetupSettings{}, errors.New("nameserver_1 must be a valid hostname")
	}
	if strings.TrimSpace(raw.Nameserver2) != "" && in.Nameserver2 == "" {
		return SetupSettings{}, errors.New("nameserver_2 must be a valid hostname")
	}
	in.UpdatedAt = time.Now().UTC()
	if err := in.Validate(); err != nil {
		return SetupSettings{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return SetupSettings{}, err
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return SetupSettings{}, err
	}
	if err := os.WriteFile(s.path, b, 0o640); err != nil {
		return SetupSettings{}, err
	}
	return in, nil
}

func Defaults() SetupSettings {
	return SetupSettings{
		DNSMode:        "external",
		Webserver:      "nginx",
		Database:       "mariadb",
		EmailEnabled:   false,
		BackupPath:     "/backups",
		PanelSSLStatus: "not_configured",
	}
}

func Suggestions(panelURL string) map[string]string {
	host := normalizeHost(panelURL)
	root := rootDomain(host)
	if root == "" {
		root = host
	}
	return map[string]string{
		"webmail_url":    "webmail." + root,
		"phpmyadmin_url": "phpmyadmin." + root,
		"nameserver_1":   "ns1." + root,
		"nameserver_2":   "ns2." + root,
	}
}

func Normalize(in SetupSettings) SetupSettings {
	in = withDefaults(in)
	in.PanelURL = normalizeHost(in.PanelURL)
	in.WebmailURL = normalizeHost(in.WebmailURL)
	in.PhpMyAdminURL = normalizeHost(in.PhpMyAdminURL)
	in.Nameserver1 = normalizeHost(in.Nameserver1)
	in.Nameserver2 = normalizeHost(in.Nameserver2)
	if in.PanelURL != "" {
		suggested := Suggestions(in.PanelURL)
		if in.WebmailURL == "" {
			in.WebmailURL = suggested["webmail_url"]
		}
		if in.PhpMyAdminURL == "" {
			in.PhpMyAdminURL = suggested["phpmyadmin_url"]
		}
		if in.Nameserver1 == "" {
			in.Nameserver1 = suggested["nameserver_1"]
		}
		if in.Nameserver2 == "" {
			in.Nameserver2 = suggested["nameserver_2"]
		}
	}
	if in.PanelSSLStatus == "" {
		in.PanelSSLStatus = "not_configured"
	}
	return in
}

func NormalizeHost(value string) string { return normalizeHost(value) }
func RootDomain(host string) string     { return rootDomain(host) }

func (s SetupSettings) Validate() error {
	if s.PanelURL != "" && normalizeHost(s.PanelURL) == "" {
		return errors.New("panel_url must be a valid hostname")
	}
	if s.DNSMode != "external" && s.DNSMode != "managed" {
		return errors.New("dns_mode must be external or managed")
	}
	if s.Webserver != "nginx" && s.Webserver != "apache" && s.Webserver != "openlitespeed" && s.Webserver != "litespeed" {
		return errors.New("unsupported webserver")
	}
	if s.Database != "mariadb" && s.Database != "mysql" && s.Database != "postgresql" {
		return errors.New("unsupported database")
	}
	if s.PanelSSLStatus != "not_configured" && s.PanelSSLStatus != "pending" && s.PanelSSLStatus != "active" && s.PanelSSLStatus != "needs_dns" && s.PanelSSLStatus != "self_signed" && s.PanelSSLStatus != "failed" {
		return errors.New("unsupported panel_ssl_status")
	}
	if !strings.HasPrefix(s.BackupPath, "/") {
		return errors.New("backup_path must be absolute")
	}
	return nil
}

func withDefaults(in SetupSettings) SetupSettings {
	d := Defaults()
	if in.DNSMode == "" {
		in.DNSMode = d.DNSMode
	}
	if in.Webserver == "" {
		in.Webserver = d.Webserver
	}
	if in.Database == "" {
		in.Database = d.Database
	}
	if in.BackupPath == "" {
		in.BackupPath = d.BackupPath
	}
	return in
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	if h, _, err := net.SplitHostPort(value); err == nil {
		value = h
	}
	value = strings.Trim(value, ".")
	if value == "" || strings.ContainsAny(value, " /\\") {
		return ""
	}
	return value
}

func rootDomain(host string) string {
	host = normalizeHost(host)
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}
