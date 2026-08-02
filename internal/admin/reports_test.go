package admin

import (
	"net/http/httptest"
	"testing"
)

func TestParseReportFilter(t *testing.T) {
	req := httptest.NewRequest("GET", "/?from=2026-07-01&to=2026-07-31", nil)
	filter, err := parseReportFilter(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := int(filter.To.Sub(filter.From).Hours() / 24); got != 31 {
		t.Fatalf("expected inclusive 31-day range, got %d", got)
	}

	req = httptest.NewRequest("GET", "/?from=2025-01-01&to=2026-07-31", nil)
	if _, err := parseReportFilter(req); err == nil {
		t.Fatal("expected ranges over 366 days to be rejected")
	}
}
