package contextmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/review-fix-agent/rfa/internal/permission"
)

func TestLoadProjectPromptsOverride(t *testing.T) {
	cwd := t.TempDir()
	rfaDir := filepath.Join(cwd, ".rfa")
	if err := os.MkdirAll(rfaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Override only one field; the rest must fall back to embedded defaults.
	override := `{"system_prompt_review":"CUSTOM-REVIEW-SYSTEM"}`
	if err := os.WriteFile(filepath.Join(rfaDir, "prompts.json"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	ps := LoadProjectPrompts(cwd)
	if ps.SystemPromptReview != "CUSTOM-REVIEW-SYSTEM" {
		t.Errorf("override not applied: %q", ps.SystemPromptReview)
	}
	if ps.SystemPromptFix == "" || ps.ReviewInstructions == "" || ps.FixInstructions == "" {
		t.Errorf("non-overridden fields should keep embedded defaults: %+v", ps)
	}
}

func TestLoadProjectPromptsDefaultsWhenAbsent(t *testing.T) {
	ps := LoadProjectPrompts(t.TempDir())
	base := loadPrompts()
	if ps != base {
		t.Errorf("absent override should equal embedded defaults")
	}
}

// TestBuildUsesProjectPromptOverride verifies the override actually reaches the
// assembled system prompt via Build (the wiring that was previously missing).
func TestBuildUsesProjectPromptOverride(t *testing.T) {
	cwd := t.TempDir()
	rfaDir := filepath.Join(cwd, ".rfa")
	if err := os.MkdirAll(rfaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `{"system_prompt_review":"SENTINEL-REVIEW-PROMPT"}`
	if err := os.WriteFile(filepath.Join(rfaDir, "prompts.json"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	built, err := NewManager(cwd).Build(context.Background(), Scope{Mode: permission.ModeReview})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(built.System, "SENTINEL-REVIEW-PROMPT") {
		t.Errorf("Build did not apply project prompt override:\n%s", built.System)
	}
}
