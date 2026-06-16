package review

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/contextmgr"
	"github.com/review-fix-agent/rfa/internal/message"
	"github.com/review-fix-agent/rfa/internal/model"
)

// fileFindingClient is a stateless model.Client that returns one high finding
// for whichever file the per-file review prompt names. Being stateless, it is
// safe under the concurrent calls ParallelReview makes (unlike model.Mock,
// whose turn counter is not synchronized).
type fileFindingClient struct{}

func (fileFindingClient) Name() string { return "filefinding" }

func (fileFindingClient) Stream(_ context.Context, req model.Request, _ func(model.StreamEvent)) (message.Message, message.Usage, error) {
	path := "unknown"
	for _, m := range req.Messages {
		if i := strings.Index(m.Text(), "## 文件: "); i >= 0 {
			rest := m.Text()[i+len("## 文件: "):]
			path = strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
			break
		}
	}
	js := fmt.Sprintf(`[{"severity":"high","file":%q,"line":1,"title":"t","evidence":"e","impact":"i"}]`, path)
	return message.NewAssistantText(js), message.Usage{}, nil
}

// TestParallelReviewDeterministicOrder asserts the merged findings come back in
// a stable, sorted order even though per-file reviews complete in nondeterministic
// goroutine order.
func TestParallelReviewDeterministicOrder(t *testing.T) {
	changed := []contextmgr.ChangedFile{
		{NewPath: "c.go"}, {NewPath: "a.go"}, {NewPath: "b.go"},
	}
	diffByFile := map[string]string{
		"a.go": "diff --git a/a.go b/a.go\n+++ b/a.go\n@@ -1 +1 @@\n+x\n",
		"b.go": "diff --git a/b.go b/b.go\n+++ b/b.go\n@@ -1 +1 @@\n+y\n",
		"c.go": "diff --git a/c.go b/c.go\n+++ b/c.go\n@@ -1 +1 @@\n+z\n",
	}
	cfg := ParallelConfig{Client: fileFindingClient{}, ModelID: "m", Concurrency: 3}

	// Run several times; order must be identical and sorted by file each time.
	for iter := 0; iter < 5; iter++ {
		rep, err := ParallelReview(context.Background(), cfg, changed, diffByFile, "")
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if len(rep.Findings) != 3 {
			t.Fatalf("iter %d: expected 3 findings, got %d", iter, len(rep.Findings))
		}
		got := []string{rep.Findings[0].File, rep.Findings[1].File, rep.Findings[2].File}
		want := []string{"a.go", "b.go", "c.go"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d: order = %v, want %v", iter, got, want)
			}
		}
	}
}
