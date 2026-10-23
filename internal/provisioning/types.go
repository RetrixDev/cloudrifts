package provisioning

import "time"

type Mode string

const (
	ModeAuto   Mode = "auto"
	ModeReal   Mode = "real"
	ModeDryRun Mode = "dry-run"
)

type WebsiteSpec struct {
	JobID            string `json:"job_id"`
	SiteID           string `json:"site_id"`
	Domain           string `json:"domain"`
	App              string `json:"app"`
	PHPVersion       string `json:"php_version"`
	Webserver        string `json:"webserver"`
	SystemUser       string `json:"system_user"`
	SystemGroup      string `json:"system_group"`
	HomeDir          string `json:"home_dir"`
	DocumentRoot     string `json:"document_root"`
	LogDir           string `json:"log_dir"`
	PublicIP         string `json:"public_ip"`
	DNSOK            bool   `json:"dns_ok"`
	RequiredAName    string `json:"required_a_name"`
	RequiredAValue   string `json:"required_a_value"`
	DatabaseName     string `json:"database_name,omitempty"`
	DatabaseUser     string `json:"database_user,omitempty"`
	DatabasePassword string `json:"database_password,omitempty"`
	AppAdminUser     string `json:"app_admin_user,omitempty"`
	AppAdminPassword string `json:"app_admin_password,omitempty"`
	AppAdminEmail    string `json:"app_admin_email,omitempty"`
	AppTitle         string `json:"app_title,omitempty"`
}

type Issue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Title     string `json:"title"`
	Detail    string `json:"detail"`
	Fix       string `json:"fix"`
	Retryable bool   `json:"retryable"`
}

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepWarning StepStatus = "warning"
	StepFailed  StepStatus = "failed"
)

type StepResult struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Detail    string     `json:"detail"`
	Status    StepStatus `json:"status"`
	Issues    []Issue    `json:"issues,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

type ResultStatus string

const (
	ResultDone    ResultStatus = "done"
	ResultWarning ResultStatus = "warning"
	ResultFailed  ResultStatus = "failed"
)

type Result struct {
	Status          ResultStatus `json:"status"`
	Steps           []StepResult `json:"steps"`
	Issues          []Issue      `json:"issues"`
	Logs            []string     `json:"logs"`
	Rollback        []string     `json:"rollback"`
	SSLIssued       bool         `json:"ssl_issued"`
	Certificate     string       `json:"certificate,omitempty"`
	DatabaseCreated bool         `json:"database_created"`
	AppInstalled    bool         `json:"app_installed"`
	DryRun          bool         `json:"dry_run"`
}

type RunOptions struct {
	Mode      Mode
	AgentPath string
	SudoPath  string
	DataDir   string
}
