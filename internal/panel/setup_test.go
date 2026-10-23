package panel

import (
	"testing"
	"time"

	"github.com/cloudrifts/panel/internal/settings"
)

func TestSetupDNSZoneRecordsComeFromPanelInput(t *testing.T) {
	setup := settings.Normalize(settings.SetupSettings{PanelURL: "panel.customer-example.tech", DNSMode: "managed"})
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	records := setupDNSZoneRecords(setup, "203.0.113.24", now)
	want := map[string]string{
		"A @":          "203.0.113.24",
		"A panel":      "203.0.113.24",
		"A ns1":        "203.0.113.24",
		"A ns2":        "203.0.113.24",
		"A webmail":    "203.0.113.24",
		"A phpmyadmin": "203.0.113.24",
		"A mail":       "203.0.113.24",
		"MX @":         "10 mail.customer-example.tech",
	}
	got := map[string]string{}
	for _, record := range records {
		got[record.Type+" "+record.Name] = record.Value
		if record.Value == "" || record.Name == "" {
			t.Fatalf("empty DNS record generated: %#v", record)
		}
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("record %s = %q, want %q; all records %#v", key, got[key], value, got)
		}
	}
}

func TestSetupDelegationRecordsComeFromPanelInput(t *testing.T) {
	setup := settings.Normalize(settings.SetupSettings{PanelURL: "panel.customer-example.tech", DNSMode: "managed"})
	records := setupDelegationRecords(setup, "203.0.113.24")
	want := []DelegationRecord{
		{Type: "GLUE", Name: "ns1.customer-example.tech", Value: "203.0.113.24"},
		{Type: "GLUE", Name: "ns2.customer-example.tech", Value: "203.0.113.24"},
		{Type: "NS", Name: "customer-example.tech", Value: "ns1.customer-example.tech"},
		{Type: "NS", Name: "customer-example.tech", Value: "ns2.customer-example.tech"},
		{Type: "A", Name: "panel.customer-example.tech", Value: "203.0.113.24"},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d delegation records, want %d: %#v", len(records), len(want), records)
	}
	for i := range want {
		if records[i].Type != want[i].Type || records[i].Name != want[i].Name || records[i].Value != want[i].Value {
			t.Fatalf("record %d = %#v, want %#v", i, records[i], want[i])
		}
	}
}
