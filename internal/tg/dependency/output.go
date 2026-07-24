// Package dependency resolves Terragrunt dependency outputs on explicit user request.
package dependency

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	terragruntpath "terragrunt-ls/internal/tg/path"
	"terragrunt-ls/internal/tg/store"
)

const maxDisplayedCommandOutput = 8 * 1024

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// Output contains formatted dependency output JSON and its Terragrunt target.
type Output struct {
	Target string
	JSON   []byte
}

// Runner executes the official Terragrunt binary with injectable process hooks.
type Runner struct {
	LookPath       func(string) (string, error)
	CommandContext func(context.Context, string, ...string) *exec.Cmd
	Timeout        time.Duration
}

// NewRunner creates a process runner with the supplied execution timeout.
func NewRunner(timeout time.Duration) Runner {
	return Runner{
		Timeout:        timeout,
		LookPath:       exec.LookPath,
		CommandContext: exec.CommandContext,
	}
}

// Resolve runs `terragrunt output -json` for a named dependency.
func (r Runner) Resolve(ctx context.Context, sourceFile, dependencyName string, st store.Store) (Output, error) {
	configPath, err := terragruntpath.DependencyConfig(st, dependencyName)
	if err != nil {
		return Output{}, fmt.Errorf("resolve dependency %q config path: %w", dependencyName, err)
	}

	target, err := terragruntpath.DependencyTarget(sourceFile, configPath)
	if err != nil {
		return Output{}, fmt.Errorf("resolve dependency %q target: %w", dependencyName, err)
	}

	lookPath := r.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	binary, err := lookPath("terragrunt")
	if err != nil {
		return Output{}, fmt.Errorf("find terragrunt executable: %w", err)
	}

	commandContext := r.CommandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}

	commandCtx := ctx

	cancel := func() {}
	if r.Timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, r.Timeout)
	}
	defer cancel()

	cmd := commandContext(commandCtx, binary, "output", "-json", "--config", target)
	cmd.Dir = filepath.Dir(target)

	combined, err := cmd.CombinedOutput()
	if err != nil {
		if contextErr := commandCtx.Err(); contextErr != nil {
			return Output{}, fmt.Errorf("resolve dependency %q outputs: %w", dependencyName, contextErr)
		}

		return Output{}, fmt.Errorf("resolve dependency %q outputs: %s: %w", dependencyName, commandError(combined), err)
	}

	var decoded any

	trimmed := bytes.TrimSpace(combined)
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return Output{}, fmt.Errorf("dependency %q returned invalid JSON: %w", dependencyName, err)
	}

	var formatted bytes.Buffer
	if err := json.Indent(&formatted, trimmed, "", "  "); err != nil {
		return Output{}, fmt.Errorf("format dependency %q JSON: %w", dependencyName, err)
	}

	formatted.WriteByte('\n')

	return Output{JSON: formatted.Bytes(), Target: target}, nil
}

func commandError(output []byte) string {
	cleaned := strings.TrimSpace(ansiEscape.ReplaceAllString(string(output), ""))
	if len(cleaned) > maxDisplayedCommandOutput {
		cleaned = cleaned[len(cleaned)-maxDisplayedCommandOutput:]
	}

	lines := strings.Split(cleaned, "\n")
	last := "Terragrunt command failed"

	for i := len(lines) - 1; i >= 0; i-- {
		if candidate := strings.TrimSpace(lines[i]); candidate != "" {
			last = candidate
			break
		}
	}

	lower := strings.ToLower(cleaned)
	switch {
	case strings.Contains(lower, "backend") && strings.Contains(lower, "init"):
		return "backend initialization is required; " + last
	case strings.Contains(lower, "dependency"):
		return "a nested dependency failed; " + last
	default:
		return last
	}
}
