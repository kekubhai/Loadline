package workspace

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// TestNewIDIsCanonicalUUIDV4 checks the shape every id the API generates and
// validates must have.
func TestNewIDIsCanonicalUUIDV4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id := NewID()
		if err := ValidateID(id); err != nil {
			t.Fatalf("NewID produced an invalid id %q: %v", id, err)
		}
		if id[14] != '4' {
			t.Fatalf("id %q is not version 4 (character 14 = %q)", id, id[14])
		}
		if !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("id %q has no RFC 4122 variant (character 19 = %q)", id, id[19])
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q", id)
		}
		seen[id] = true
	}
}

func TestValidateID(t *testing.T) {
	valid := []string{
		"f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"00000000-0000-4000-8000-000000000000",
		"F47AC10B-58CC-4372-A567-0E02B2C3D479",
	}
	for _, id := range valid {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q): unexpected error %v", id, err)
		}
	}
	invalid := []string{
		"",
		"sim-1",
		"f47ac10b58cc4372a5670e02b2c3d479",    // no hyphens
		"f47ac10b-58cc-4372-a567-0e02b2c3d47", // too short
		"f47ac10b-58cc-4372-a567-0e02b2c3d479f",
		"f47ac10b-58cc-4372-a567-0e02b2c3d47g", // non-hex
		"'; DROP TABLE projects; --",
	}
	for _, id := range invalid {
		err := ValidateID(id)
		if err == nil {
			t.Errorf("ValidateID(%q): expected an error", id)
			continue
		}
		if !errors.Is(err, ErrInvalidID) {
			t.Errorf("ValidateID(%q): error %v does not wrap ErrInvalidID", id, err)
		}
	}
}

// TestSimulationRunValidate covers the guards that keep an unstorable run
// out of the database, in particular a seed that would silently truncate.
func TestSimulationRunValidate(t *testing.T) {
	versionID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	base := SimulationRun{
		ArchitectureVersionID: versionID,
		Status:                RunStatusCompleted,
		Seed:                  42,
		DurationMS:            60_000,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}

	tooBig := base
	tooBig.Seed = uint64(math.MaxInt64) + 1
	if err := tooBig.Validate(); err == nil {
		t.Error("expected a seed beyond bigint to be rejected")
	}

	badStatus := base
	badStatus.Status = "paused"
	if err := badStatus.Validate(); err == nil {
		t.Error("expected a non-persistable status to be rejected")
	}

	noVersion := base
	noVersion.ArchitectureVersionID = ""
	if err := noVersion.Validate(); err == nil {
		t.Error("expected a missing architecture version id to be rejected")
	}

	badDuration := base
	badDuration.DurationMS = math.NaN()
	if err := badDuration.Validate(); err == nil {
		t.Error("expected a NaN duration to be rejected")
	}
}

func TestProjectAndWorkloadValidate(t *testing.T) {
	if err := (Project{Name: "  "}).Validate(); err == nil {
		t.Error("expected a blank project name to be rejected")
	}
	if err := (Project{Name: "prod"}).Validate(); err != nil {
		t.Errorf("valid project rejected: %v", err)
	}

	w := Workload{
		ArchitectureVersionID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Name:                  "peak",
		Configuration:         json.RawMessage(`{"dau":1000}`),
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("valid workload rejected: %v", err)
	}
	notAnObject := w
	notAnObject.Configuration = json.RawMessage(`[1,2,3]`)
	if err := notAnObject.Validate(); err == nil {
		t.Error("expected a non-object configuration to be rejected")
	}
}

// TestRunStatusValid pins the persistable lifecycle: transient control
// states live in the running process and must not be storable.
func TestRunStatusValid(t *testing.T) {
	for _, s := range []RunStatus{RunStatusPending, RunStatusRunning, RunStatusCompleted, RunStatusFailed, RunStatusStopped} {
		if !s.Valid() {
			t.Errorf("status %q should be valid", s)
		}
	}
	for _, s := range []RunStatus{"", "paused", "stopping", "COMPLETED"} {
		if s.Valid() {
			t.Errorf("status %q should be rejected", s)
		}
	}
}
