package review

import (
	"context"
	"errors"
	"testing"

	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
)

// errClient is a model.Client whose Stream always fails, to exercise the
// degraded path of FilterFindings.
type errClient struct{}

func (errClient) Name() string { return "err" }
func (errClient) Stream(context.Context, model.Request, func(model.StreamEvent)) (message.Message, message.Usage, error) {
	return message.Message{}, message.Usage{}, errors.New("boom")
}

func TestFilterFindingsReturnsErrorOnClientFailure(t *testing.T) {
	r := Report{Findings: []Finding{{File: "a.go", Line: 1, Severity: "high", Title: "t", Evidence: "e", Impact: "i"}}}
	got, err := FilterFindings(context.Background(), errClient{}, "m", r, "some diff")
	if err == nil {
		t.Fatal("expected non-nil error when the filter model call fails")
	}
	// On failure the report must come back unfiltered (unchanged).
	if len(got.Findings) != 1 {
		t.Errorf("expected unfiltered report (1 finding), got %d", len(got.Findings))
	}
}

func TestFilterFindingsNoopIsNoError(t *testing.T) {
	// No findings: nothing to filter, no model call, no error.
	if _, err := FilterFindings(context.Background(), errClient{}, "m", Report{}, "diff"); err != nil {
		t.Errorf("empty report should be a no-op, got err %v", err)
	}
	// No diff: same.
	r := Report{Findings: []Finding{{File: "a.go", Title: "t"}}}
	if _, err := FilterFindings(context.Background(), errClient{}, "m", r, ""); err != nil {
		t.Errorf("empty diff should be a no-op, got err %v", err)
	}
}
