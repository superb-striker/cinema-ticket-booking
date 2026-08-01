package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidCinema(t *testing.T) {
	valid := Cinema{
		Name: "Central Cinema", Timezone: "Asia/Kolkata", AddressLine: "1 Main Road",
		City: "Bengaluru", Region: "Karnataka", PostalCode: "560001",
		CutoffMinutes: 120, RefundPercent: 75,
	}
	if !validCinema(valid) {
		t.Fatal("expected cinema to be valid")
	}
	valid.Timezone = "not/a-timezone"
	if validCinema(valid) {
		t.Fatal("expected invalid timezone to be rejected")
	}
}

func TestValidShow(t *testing.T) {
	start := time.Now().Add(24 * time.Hour)
	valid := ShowInput{
		MovieID: uuid.New(), AuditoriumID: uuid.New(), StartsAt: start,
		SalesEndAt: start.Add(-15 * time.Minute), BasePricePaise: 25000,
		Currency: "INR", Status: "scheduled",
	}
	if !validShow(valid) {
		t.Fatal("expected show to be valid")
	}
	valid.SalesEndAt = start.Add(time.Minute)
	if validShow(valid) {
		t.Fatal("expected sales end after start to be rejected")
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"title":"Example","unexpected":true}`))
	recorder := httptest.NewRecorder()
	var movie Movie
	if decode(recorder, req, &movie) {
		t.Fatal("expected unknown field to be rejected")
	}
	if recorder.Code != 400 {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}
