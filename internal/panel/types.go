package panel

import "time"

type WebsiteApp string

const (
	AppWordPress   WebsiteApp = "wordpress"
	AppWooCommerce WebsiteApp = "woocommerce"
	AppPHP         WebsiteApp = "php"
)

type WebsiteStatus string

const (
	WebsiteProvisioning WebsiteStatus = "provisioning"
	WebsiteActive       WebsiteStatus = "active"
	WebsiteAttention    WebsiteStatus = "attention"
	WebsiteFailed       WebsiteStatus = "failed"
)

type DNSRecord struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type DNSZoneRecord struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	TTL       int       `json:"ttl"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DNSZone struct {
	ID            string          `json:"id"`
	Domain        string          `json:"domain"`
	Mode          string          `json:"mode"`
	Status        string          `json:"status"`
	ServerIP      string          `json:"server_ip"`
	Nameserver1   string          `json:"nameserver_1"`
	Nameserver2   string          `json:"nameserver_2"`
	Records       []DNSZoneRecord `json:"records"`
	Issues        []WebsiteIssue  `json:"issues"`
	LastJobID     string          `json:"last_job_id,omitempty"`
	LastAppliedAt *time.Time      `json:"last_applied_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type DNSRoleState struct {
	Mode        string    `json:"mode"`
	Status      string    `json:"status"`
	Installed   bool      `json:"installed"`
	Ready       bool      `json:"ready"`
	ServerIP    string    `json:"server_ip"`
	Nameserver1 string    `json:"nameserver_1"`
	Nameserver2 string    `json:"nameserver_2"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type DNSZonesResponse struct {
	Role  DNSRoleState `json:"role"`
	Zones []DNSZone    `json:"zones"`
}

type CreateDNSZoneRequest struct {
	Domain    string          `json:"domain"`
	WebsiteID string          `json:"website_id,omitempty"`
	Records   []DNSZoneRecord `json:"records,omitempty"`
}

type UpsertDNSRecordRequest struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

type SetupDNSRoleResponse struct {
	Role DNSRoleState `json:"role"`
	Job  Job          `json:"job"`
}

type MailRoleState struct {
	Enabled    bool      `json:"enabled"`
	Status     string    `json:"status"`
	Installed  bool      `json:"installed"`
	Ready      bool      `json:"ready"`
	Hostname   string    `json:"hostname"`
	WebmailURL string    `json:"webmail_url"`
	ServerIP   string    `json:"server_ip"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type SetupMailRoleResponse struct {
	Role MailRoleState `json:"role"`
	Job  Job           `json:"job"`
}

type ApplyDNSZoneResponse struct {
	Zone DNSZone `json:"zone"`
	Job  Job     `json:"job"`
}

type UpsertDNSRecordResponse struct {
	Zone   DNSZone       `json:"zone"`
	Record DNSZoneRecord `json:"record"`
	Job    Job           `json:"job"`
}

type WebsiteIssue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Fix       string `json:"fix"`
	Retryable bool   `json:"retryable"`
}

type SSLState struct {
	Provider      string     `json:"provider"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	CanRetry      bool       `json:"can_retry"`
	RetryCount    int        `json:"retry_count"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
}

type WebsiteAction struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

type Website struct {
	ID               string          `json:"id"`
	Domain           string          `json:"domain"`
	App              WebsiteApp      `json:"app"`
	Kind             string          `json:"kind"`
	SystemUser       string          `json:"system_user,omitempty"`
	HomeDir          string          `json:"home_dir,omitempty"`
	DocumentRoot     string          `json:"document_root"`
	PHPVersion       string          `json:"php_version"`
	RuntimeStatus    string          `json:"runtime_status,omitempty"`
	WebserverStatus  string          `json:"webserver_status,omitempty"`
	DatabaseName     string          `json:"database_name,omitempty"`
	DatabaseUser     string          `json:"database_user,omitempty"`
	DatabasePassword string          `json:"database_password,omitempty"`
	DatabaseStatus   string          `json:"database_status,omitempty"`
	AppStatus        string          `json:"app_status,omitempty"`
	AppAdminUser     string          `json:"app_admin_user,omitempty"`
	AppAdminEmail    string          `json:"app_admin_email,omitempty"`
	AppAdminPassword string          `json:"app_admin_password,omitempty"`
	StorageBytes     int64           `json:"storage_bytes"`
	Healthy          bool            `json:"healthy"`
	DNSStatus        string          `json:"dns_status"`
	SSLStatus        string          `json:"ssl_status"`
	SSL              SSLState        `json:"ssl"`
	RequiredRecords  []DNSRecord     `json:"required_records"`
	Issues           []WebsiteIssue  `json:"issues"`
	NextActions      []WebsiteAction `json:"next_actions"`
	LastJobID        string          `json:"last_job_id,omitempty"`
	Status           WebsiteStatus   `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type CreateWebsiteRequest struct {
	Domain     string     `json:"domain"`
	App        WebsiteApp `json:"app"`
	PHPVersion string     `json:"php_version"`
}

type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

type JobStepStatus string

const (
	StepPending JobStepStatus = "pending"
	StepRunning JobStepStatus = "running"
	StepDone    JobStepStatus = "done"
	StepWarning JobStepStatus = "warning"
	StepFailed  JobStepStatus = "failed"
)

type JobStep struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail"`
	Status    JobStepStatus  `json:"status"`
	Issues    []WebsiteIssue `json:"issues,omitempty"`
	StartedAt *time.Time     `json:"started_at,omitempty"`
	EndedAt   *time.Time     `json:"ended_at,omitempty"`
}

type Job struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	TargetID   string         `json:"target_id,omitempty"`
	TargetName string         `json:"target_name,omitempty"`
	Status     JobStatus      `json:"status"`
	Steps      []JobStep      `json:"steps"`
	Issues     []WebsiteIssue `json:"issues"`
	Logs       []string       `json:"logs"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type DiagnosticCheck struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type WebsiteDiagnostics struct {
	Website        Website           `json:"website"`
	Checks         []DiagnosticCheck `json:"checks"`
	RequiredRecord []DNSRecord       `json:"required_records"`
	CanRetrySSL    bool              `json:"can_retry_ssl"`
}

type Backup struct {
	ID               string         `json:"id"`
	WebsiteID        string         `json:"website_id"`
	WebsiteDomain    string         `json:"website_domain"`
	Type             string         `json:"type"`
	Status           string         `json:"status"`
	Path             string         `json:"path,omitempty"`
	SizeBytes        int64          `json:"size_bytes"`
	IncludesFiles    bool           `json:"includes_files"`
	IncludesDatabase bool           `json:"includes_database"`
	JobID            string         `json:"job_id,omitempty"`
	LastRestoreJobID string         `json:"last_restore_job_id,omitempty"`
	Issues           []WebsiteIssue `json:"issues"`
	Logs             []string       `json:"logs"`
	CreatedAt        time.Time      `json:"created_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type ActivityEvent struct {
	ID        string    `json:"id"`
	Icon      string    `json:"icon"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Tone      string    `json:"tone"`
	CreatedAt time.Time `json:"created_at"`
}

type WebserverOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Default     bool   `json:"default"`
	Current     bool   `json:"current"`
	Installed   bool   `json:"installed"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

type ServerRole struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Version     string    `json:"version"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SetupStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Done   bool   `json:"done"`
	Action string `json:"action"`
}

type ServerSummary struct {
	Name       string `json:"name"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Region     string `json:"region"`
	IPv4       string `json:"ipv4"`
	Mode       string `json:"mode"`
	Webserver  string `json:"webserver"`
	Database   string `json:"database"`
	DNSMode    string `json:"dns_mode"`
	BackupPath string `json:"backup_path"`
}

type Overview struct {
	Server     ServerSummary   `json:"server"`
	Websites   []Website       `json:"websites"`
	Jobs       []Job           `json:"jobs"`
	Activity   []ActivityEvent `json:"activity"`
	Roles      []ServerRole    `json:"roles"`
	SetupSteps []SetupStep     `json:"setup_steps"`
}

type CreateWebsiteResponse struct {
	Website Website `json:"website"`
	Job     Job     `json:"job"`
}

type RetrySSLResponse struct {
	Website Website `json:"website"`
	Job     Job     `json:"job"`
}

type SwitchWebserverRequest struct {
	Webserver string `json:"webserver"`
}

type SwitchWebserverResponse struct {
	From       string            `json:"from"`
	To         string            `json:"to"`
	Job        Job               `json:"job"`
	Webservers []WebserverOption `json:"webservers"`
}
