package config

import "os"

type Config struct {
	HTTPAddr      string `json:"http_addr"`
	DataDir       string `json:"data_dir"`
	EtcDir        string `json:"etc_dir"`
	LogDir        string `json:"log_dir"`
	WebDir        string `json:"web_dir"`
	ExecutionMode string `json:"execution_mode"`
	AgentPath     string `json:"agent_path"`
	SudoPath      string `json:"sudo_path"`
}

func LoadDefault() Config {
	return Config{
		HTTPAddr:      env("CLOUDRIFTS_HTTP_ADDR", ":8080"),
		DataDir:       env("CLOUDRIFTS_DATA_DIR", "/var/lib/cloudrifts"),
		EtcDir:        env("CLOUDRIFTS_ETC_DIR", "/etc/cloudrifts"),
		LogDir:        env("CLOUDRIFTS_LOG_DIR", "/var/log/cloudrifts"),
		WebDir:        env("CLOUDRIFTS_WEB_DIR", "/usr/local/share/cloudrifts/web"),
		ExecutionMode: env("CLOUDRIFTS_EXECUTION_MODE", "auto"),
		AgentPath:     env("CLOUDRIFTS_AGENT_PATH", "/usr/local/bin/cloudrifts-agent"),
		SudoPath:      env("CLOUDRIFTS_SUDO_PATH", "/usr/bin/sudo"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
