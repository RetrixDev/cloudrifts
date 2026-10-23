package provisioning

import (
	"context"
	"testing"
)

func TestApplySetupPanelDryRunWaitsForDNSBeforeTrustedSSL(t *testing.T) {
	result := ApplySetupPanel(context.Background(), PanelSpec{PanelURL: "panel.example.invalid", ServerIP: "203.0.113.10"}, true)
	if result.Status != ResultWarning {
		t.Fatalf("expected warning result while DNS is not delegated, got %s", result.Status)
	}
	if result.SSLIssued {
		t.Fatal("dry-run must not mark trusted SSL issued when panel DNS does not resolve to the server")
	}
	if result.Certificate != snakeoilCertPath() {
		t.Fatalf("expected fallback certificate %q, got %q", snakeoilCertPath(), result.Certificate)
	}
	var foundDNSWarning, foundSSLWarning bool
	for _, issue := range result.Issues {
		if issue.Code == "panel_dns_delegation_required" {
			foundDNSWarning = true
		}
		if issue.Code == "panel_ssl_waiting_for_dns" {
			foundSSLWarning = true
		}
	}
	if !foundDNSWarning || !foundSSLWarning {
		t.Fatalf("expected DNS and SSL propagation warnings, got %#v", result.Issues)
	}
}
