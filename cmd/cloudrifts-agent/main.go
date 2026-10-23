package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cloudrifts/panel/internal/provisioning"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "apply-website":
		applyWebsite(os.Args[2:])
	case "switch-webserver":
		switchWebserver(os.Args[2:])
	case "restore-backup":
		restoreBackup(os.Args[2:])
	case "setup-dns-role":
		setupDNSRole(os.Args[2:])
	case "apply-dns-zone":
		applyDNSZone(os.Args[2:])
	case "setup-mail-role":
		setupMailRole(os.Args[2:])
	case "setup-panel":
		setupPanel(os.Args[2:])
	case "version":
		fmt.Println("cloudrifts-agent 0.1.0-dev")
	default:
		usage()
		os.Exit(2)
	}
}

func setupPanel(args []string) {
	fs := flag.NewFlagSet("setup-panel", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts panel setup spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.PanelSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result := provisioning.ApplySetupPanel(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func setupMailRole(args []string) {
	fs := flag.NewFlagSet("setup-mail-role", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts mail role spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.MailRoleSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result := provisioning.ApplySetupMailRole(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func setupDNSRole(args []string) {
	fs := flag.NewFlagSet("setup-dns-role", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts DNS role spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.DNSRoleSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result := provisioning.ApplySetupDNSRole(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func applyDNSZone(args []string) {
	fs := flag.NewFlagSet("apply-dns-zone", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts DNS zone spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.DNSZoneSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result := provisioning.ApplyDNSZone(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func switchWebserver(args []string) {
	fs := flag.NewFlagSet("switch-webserver", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts webserver switch spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.SwitchWebserverSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result := provisioning.ApplySwitchWebserver(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func restoreBackup(args []string) {
	fs := flag.NewFlagSet("restore-backup", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts restore spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.RestoreBackupSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result := provisioning.ApplyRestoreBackup(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func applyWebsite(args []string) {
	fs := flag.NewFlagSet("apply-website", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to Cloudrifts website spec JSON")
	jsonOut := fs.Bool("json", false, "print JSON result")
	dryRun := fs.Bool("dry-run", false, "validate without changing the host")
	_ = fs.Parse(args)
	if *specPath == "" {
		fatal("--spec is required")
	}
	b, err := os.ReadFile(*specPath)
	if err != nil {
		fatal(err.Error())
	}
	var spec provisioning.WebsiteSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result := provisioning.ApplyCreateWebsite(ctx, spec, *dryRun)
	printResult(result, *jsonOut)
}

func printResult(result provisioning.Result, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fatal(err.Error())
		}
		return
	}
	for _, line := range result.Logs {
		fmt.Println(line)
	}
	if result.Status == provisioning.ResultFailed {
		os.Exit(1)
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "cloudrifts-agent:", message)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `cloudrifts-agent 0.1.0-dev

Usage:
  cloudrifts-agent apply-website --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent switch-webserver --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent restore-backup --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent setup-dns-role --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent apply-dns-zone --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent setup-mail-role --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent setup-panel --spec /path/to/spec.json [--json] [--dry-run]
  cloudrifts-agent version`)
}
