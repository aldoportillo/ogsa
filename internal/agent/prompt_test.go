package agent

import (
	"strings"
	"testing"
	"time"
)

func TestSystemPromptIncludesInstructions(t *testing.T) {
	for _, hasTools := range []bool{true, false} {
		got := SystemPrompt("/p", hasTools, time.Now(), "Models live in internal/llm.")
		if !strings.Contains(got, ".ogsa/OGSA.md") || !strings.HasSuffix(got, "Models live in internal/llm.\n") {
			t.Errorf("hasTools=%v: instructions missing:\n%s", hasTools, got)
		}
	}
	if got := SystemPrompt("/p", true, time.Now(), ""); strings.Contains(got, "OGSA.md") {
		t.Error("empty instructions still added a section")
	}
}
