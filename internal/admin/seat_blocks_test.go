package admin

import (
	"testing"

	"github.com/google/uuid"
)

func TestOptionalUUID(t *testing.T) {
	if value, err := optionalUUID(""); err != nil || value != nil {
		t.Fatalf("expected an empty filter, got %v, %v", value, err)
	}
	id := uuid.New()
	value, err := optionalUUID(id.String())
	if err != nil || value != id {
		t.Fatalf("expected %s, got %v, %v", id, value, err)
	}
	if _, err := optionalUUID("not-a-uuid"); err == nil {
		t.Fatal("expected malformed UUID to be rejected")
	}
}
