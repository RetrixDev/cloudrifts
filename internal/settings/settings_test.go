package settings

import "testing"

func TestNormalizeDerivesSetupHostsFromPanelInput(t *testing.T) {
	setup := Normalize(SetupSettings{PanelURL: "https://panel.customer-example.tech/", DNSMode: "managed"})
	if setup.PanelURL != "panel.customer-example.tech" {
		t.Fatalf("panel URL was not normalized from input: %q", setup.PanelURL)
	}
	expected := map[string]string{
		"webmail": "webmail.customer-example.tech",
		"phpmy":   "phpmyadmin.customer-example.tech",
		"ns1":     "ns1.customer-example.tech",
		"ns2":     "ns2.customer-example.tech",
	}
	if setup.WebmailURL != expected["webmail"] || setup.PhpMyAdminURL != expected["phpmy"] || setup.Nameserver1 != expected["ns1"] || setup.Nameserver2 != expected["ns2"] {
		t.Fatalf("derived hosts mismatch: %#v", setup)
	}
}
