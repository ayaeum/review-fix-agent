package contextmgr

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

//go:embed prompts.json
var defaultPromptsJSON []byte

type promptSet struct {
	SystemPromptReview string `json:"system_prompt_review"`
	SystemPromptFix    string `json:"system_prompt_fix"`
	ReviewInstructions string `json:"review_instructions"`
	FixInstructions    string `json:"fix_instructions"`
}

var (
	loadedPrompts promptSet
	promptsOnce   sync.Once
)

func loadPrompts() promptSet {
	promptsOnce.Do(func() {
		if err := json.Unmarshal(defaultPromptsJSON, &loadedPrompts); err != nil {
			panic("contextmgr: invalid embedded prompts.json: " + err.Error())
		}
	})
	return loadedPrompts
}

// LoadProjectPrompts tries to load a project-specific prompts override from
// <cwd>/.rfa/prompts.json. Returns the embedded defaults for any missing field.
func LoadProjectPrompts(cwd string) promptSet {
	base := loadPrompts()
	path := filepath.Join(cwd, ".rfa", "prompts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return base
	}
	var override promptSet
	if err := json.Unmarshal(data, &override); err != nil {
		return base
	}
	if override.SystemPromptReview != "" {
		base.SystemPromptReview = override.SystemPromptReview
	}
	if override.SystemPromptFix != "" {
		base.SystemPromptFix = override.SystemPromptFix
	}
	if override.ReviewInstructions != "" {
		base.ReviewInstructions = override.ReviewInstructions
	}
	if override.FixInstructions != "" {
		base.FixInstructions = override.FixInstructions
	}
	return base
}
