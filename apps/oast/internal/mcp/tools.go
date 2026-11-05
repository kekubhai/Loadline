package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kekubhai/Loadline/apps/oast/internal/evidence"
	"github.com/kekubhai/Loadline/apps/oast/internal/registry"
	"github.com/kekubhai/Loadline/apps/oast/internal/service"
)

const (
	maxTTLSeconds  = 24 * 60 * 60
	maxWaitSeconds = 30
	waitSlice      = 250 * time.Millisecond
)

type createInput struct {
	Target           string `json:"target" jsonschema:"authorized target host or URL"`
	AuthorizationRef string `json:"authorization_ref" jsonschema:"engagement or authorization reference"`
	TTLSeconds       int    `json:"ttl_seconds,omitempty" jsonschema:"optional lifetime in seconds; defaults to the server default"`
}

type createOutput struct {
	TestID              string   `json:"test_id"`
	Target              string   `json:"target"`
	State               string   `json:"state"`
	ExpiresAt           string   `json:"expires_at"`
	HTTPCallbackURL     string   `json:"http_callback_url"`
	DNSCallbackHostname string   `json:"dns_callback_hostname"`
	Assessment          string   `json:"assessment"`
	Guidance            []string `json:"guidance"`
}

type getEvidenceInput struct {
	TestID        string  `json:"test_id" jsonschema:"test identifier returned by oast_create_test"`
	AfterSequence *uint64 `json:"after_sequence,omitempty" jsonschema:"optional cursor: return only evidence with a greater sequence"`
	WaitSeconds   int     `json:"wait_seconds,omitempty" jsonschema:"optional wait time in seconds, at most 30"`
}

type getEvidenceOutput struct {
	TestID            string            `json:"test_id"`
	State             string            `json:"state"`
	Evidence          []evidence.Record `json:"evidence"`
	NextAfterSequence *uint64           `json:"next_after_sequence,omitempty" jsonschema:"cursor to pass as after_sequence on the next call"`
}

type listInput struct {
	IncludeClosed bool `json:"include_closed,omitempty" jsonschema:"include closed and expired tests"`
	Limit         int  `json:"limit,omitempty" jsonschema:"maximum number of summaries to return; defaults to 50 and may not exceed 200"`
}

type testSummary struct {
	ID            string `json:"id"`
	Target        string `json:"target"`
	State         string `json:"state"`
	CreatedAt     string `json:"created_at"`
	ExpiresAt     string `json:"expires_at"`
	EvidenceCount int    `json:"evidence_count"`
}

type listOutput struct {
	Tests         []testSummary `json:"tests"`
	Count         int           `json:"count"`
	IncludeClosed bool          `json:"include_closed"`
}

type closeInput struct {
	TestID string `json:"test_id" jsonschema:"test identifier returned by oast_create_test"`
}

type closeOutput struct {
	TestID        string `json:"test_id"`
	State         string `json:"state"`
	EvidenceCount int    `json:"evidence_count"`
}

func (s *Server) registerTools() {
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "oast_create_test",
		Description: "Create a scoped OAST test and return HTTP/DNS callback locations. Use only against authorized targets.",
	}, s.createTest)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "oast_get_evidence",
		Description: "Return evidence recorded for a test. Supports an incremental sequence cursor and a bounded long poll.",
	}, s.getEvidence)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "oast_list_tests",
		Description: "List test metadata without callback tokens or raw evidence.",
	}, s.listTests)
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:        "oast_close_test",
		Description: "Close a test so further callbacks are rejected, returning its final state and evidence count.",
	}, s.closeTest)
}

func (s *Server) createTest(ctx context.Context, _ *sdk.CallToolRequest, in createInput) (res *sdk.CallToolResult, env envelope[createOutput], err error) {
	defer func() { s.logToolError("oast_create_test", env.Error) }()
	if in.TTLSeconds < 0 || in.TTLSeconds > maxTTLSeconds {
		return failure[createOutput](fmt.Errorf("%w: ttl_seconds must be between 0 and %d", errInvalidInput, maxTTLSeconds))
	}
	var ttl time.Duration
	if in.TTLSeconds > 0 {
		ttl = time.Duration(in.TTLSeconds) * time.Second
	}
	test, err := s.service.CreateTest(ctx, service.CreateRequest{
		Target:           in.Target,
		AuthorizationRef: in.AuthorizationRef,
		TTL:              ttl,
	})
	if err != nil {
		return failure[createOutput](err)
	}
	return success(createOutput{
		TestID:              test.ID,
		Target:              test.Target,
		State:               string(test.State),
		ExpiresAt:           test.ExpiresAt.UTC().Format(time.RFC3339),
		HTTPCallbackURL:     s.callbackURL(test.Token),
		DNSCallbackHostname: s.dnsHostname(test.Token),
		Assessment:          evidence.AssessmentObservedOnly,
		Guidance: []string{
			"Use the callback locations only within the authorized engagement.",
			"Observed callbacks show reachability, not proof of SSRF or exploitation.",
			"Poll with after_sequence to retrieve evidence incrementally.",
		},
	})
}

func (s *Server) getEvidence(ctx context.Context, _ *sdk.CallToolRequest, in getEvidenceInput) (res *sdk.CallToolResult, env envelope[getEvidenceOutput], err error) {
	defer func() { s.logToolError("oast_get_evidence", env.Error) }()
	testID := strings.TrimSpace(in.TestID)
	if testID == "" {
		return failure[getEvidenceOutput](fmt.Errorf("%w: test_id is required", errInvalidInput))
	}
	if in.WaitSeconds < 0 || in.WaitSeconds > maxWaitSeconds {
		return failure[getEvidenceOutput](fmt.Errorf("%w: wait_seconds must be between 0 and %d", errInvalidInput, maxWaitSeconds))
	}

	deadline := time.Now().Add(time.Duration(in.WaitSeconds) * time.Second)
	for {
		test, err := s.service.GetTest(ctx, testID)
		if err != nil {
			return failure[getEvidenceOutput](err)
		}
		records := filterEvidence(test.Evidence, in.AfterSequence)
		if len(records) > 0 || test.State != registry.StateActive || !time.Now().Before(deadline) {
			return success(evidenceOutput(test, records, in.AfterSequence))
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return success(evidenceOutput(test, nil, in.AfterSequence))
		}
		if remaining > waitSlice {
			remaining = waitSlice
		}
		if err := s.service.WaitForChange(ctx, remaining); err != nil {
			return failure[getEvidenceOutput](err)
		}
	}
}

func evidenceOutput(test *registry.Test, records []evidence.Record, after *uint64) getEvidenceOutput {
	if records == nil {
		records = []evidence.Record{}
	}
	next := after
	if len(records) > 0 {
		last := records[len(records)-1].Sequence
		next = &last
	}
	return getEvidenceOutput{
		TestID:            test.ID,
		State:             string(test.State),
		Evidence:          records,
		NextAfterSequence: next,
	}
}

func filterEvidence(records []evidence.Record, after *uint64) []evidence.Record {
	out := make([]evidence.Record, 0, len(records))
	for _, record := range records {
		if after != nil && record.Sequence <= *after {
			continue
		}
		out = append(out, record)
	}
	return out
}

func (s *Server) listTests(ctx context.Context, _ *sdk.CallToolRequest, in listInput) (res *sdk.CallToolResult, env envelope[listOutput], err error) {
	defer func() { s.logToolError("oast_list_tests", env.Error) }()
	if in.Limit < 0 || in.Limit > service.MaxListLimit {
		return failure[listOutput](fmt.Errorf("%w: limit must be between 0 and %d", errInvalidInput, service.MaxListLimit))
	}
	limit := in.Limit
	if limit == 0 {
		limit = service.DefaultListLimit
	}
	summaries, err := s.service.ListTests(ctx, service.ListOptions{
		IncludeClosed: in.IncludeClosed,
		Limit:         limit,
	})
	if err != nil {
		return failure[listOutput](err)
	}
	tests := make([]testSummary, 0, len(summaries))
	for _, summary := range summaries {
		tests = append(tests, testSummary{
			ID:            summary.ID,
			Target:        summary.Target,
			State:         string(summary.State),
			CreatedAt:     summary.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:     summary.ExpiresAt.UTC().Format(time.RFC3339),
			EvidenceCount: summary.EvidenceCount,
		})
	}
	return success(listOutput{
		Tests:         tests,
		Count:         len(tests),
		IncludeClosed: in.IncludeClosed,
	})
}

func (s *Server) closeTest(ctx context.Context, _ *sdk.CallToolRequest, in closeInput) (res *sdk.CallToolResult, env envelope[closeOutput], err error) {
	defer func() { s.logToolError("oast_close_test", env.Error) }()
	testID := strings.TrimSpace(in.TestID)
	if testID == "" {
		return failure[closeOutput](fmt.Errorf("%w: test_id is required", errInvalidInput))
	}
	if err := s.service.CloseTest(ctx, testID); err != nil {
		if errors.Is(err, registry.ErrExpired) {
			if test, getErr := s.service.GetTest(ctx, testID); getErr == nil {
				return failureWithData(closeResult(test), err)
			}
		}
		return failure[closeOutput](err)
	}
	test, err := s.service.GetTest(ctx, testID)
	if err != nil {
		return failure[closeOutput](err)
	}
	return success(closeResult(test))
}

func closeResult(test *registry.Test) closeOutput {
	return closeOutput{
		TestID:        test.ID,
		State:         string(test.State),
		EvidenceCount: len(test.Evidence),
	}
}
