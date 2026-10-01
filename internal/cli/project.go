package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aldoportillo/ogsa/internal/llm"
)

const instructionsFile = ".ogsa/OGSA.md"

const maxInstructions = 16 << 10

func (r *REPL) projectInstructions() string {
	if r.WorkDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(r.WorkDir, instructionsFile))
	if err != nil {
		return ""
	}
	if len(b) > maxInstructions {
		return string(b[:maxInstructions]) + "\n(truncated: keep .ogsa/OGSA.md short)"
	}
	return string(b)
}

const initPrompt = `Write ` + instructionsFile + `: a short guide to this project for an AI coding assistant, so future sessions can find their way without exploring everything.

Include:
- What the project is, in one or two sentences.
- How it's organized: the main directories and files, and what lives where.
- How to build, run and test it.
- Conventions worth following (naming, error handling, testing style).

Look at the code to get this right, but don't read every file: directory listings, entry points, build files and a few key files are enough. Keep it under about 60 lines, factual and specific. Then create the file with the write tool.`

const updatePrompt = `Update ` + instructionsFile + `, the guide to this project for an AI coding assistant. Read it first, check it against the code, then fix anything outdated or missing. Keep it under about 60 lines. Use edit for small fixes or write to replace it.`

func (r *REPL) cmdInit(string) (bool, error) {
	if r.Config.ModelInfo(r.Config.Provider, r.Config.Model).Tools == llm.ToolsUnsupported {
		return false, errors.New("/init needs a model that supports tools; pick one with /model")
	}
	prompt := initPrompt
	if r.projectInstructions() != "" {
		answer, err := r.prompt(instructionsFile + " already exists. Update it? [y/N] ")
		if err != nil || !isYes(answer) {
			return false, err
		}
		prompt = updatePrompt
	}
	fmt.Fprintf(r.Out, "asking the model to write %s; you'll review the file before it's saved\n\n", instructionsFile)
	return false, r.chat(prompt)
}
