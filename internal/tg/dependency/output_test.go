package dependency_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"terragrunt-ls/internal/tg/dependency"
	"terragrunt-ls/internal/tg/store"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

func TestRunnerResolveExecutesTerragruntAtDependencyTarget(t *testing.T) {
	binaryDir := buildFakeTerragrunt(t)
	t.Setenv("PATH", binaryDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TG_TEST_MODE", "success")
	cwdFile := filepath.Join(t.TempDir(), "cwd")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TG_TEST_CWD_FILE", cwdFile)
	t.Setenv("TG_TEST_ARGS_FILE", argsFile)

	sourcePath, targetPath, st := dependencyFixture(t)
	output, err := dependency.NewRunner(10*time.Second).Resolve(t.Context(), sourcePath, "app", st)

	require.NoError(t, err)
	assert.JSONEq(t, `{"id":{"value":"123"}}`, string(output.JSON))
	assert.Equal(t, targetPath, output.Target)
	assert.Equal(t, filepath.Dir(targetPath), readFile(t, cwdFile))
	assert.Equal(t, []string{"output", "-json", "--config", targetPath}, strings.Split(readFile(t, argsFile), "\n"))
}

func TestRunnerResolveErrors(t *testing.T) {
	t.Run("missing executable", func(t *testing.T) {
		sourcePath, _, st := dependencyFixture(t)
		runner := dependency.NewRunner(time.Second)
		runner.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }

		_, err := runner.Resolve(t.Context(), sourcePath, "app", st)
		require.Error(t, err)
		assert.ErrorIs(t, err, exec.ErrNotFound)
	})

	for _, tt := range []struct {
		name string
		mode string
		want string
	}{
		{"nonzero strips ansi", "error", "run terragrunt init before resolving outputs"},
		{"invalid json", "invalid", "invalid JSON"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binaryDir := buildFakeTerragrunt(t)
			t.Setenv("PATH", binaryDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TG_TEST_MODE", tt.mode)
			sourcePath, _, st := dependencyFixture(t)

			_, err := dependency.NewRunner(10*time.Second).Resolve(t.Context(), sourcePath, "app", st)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), "\x1b[")
		})
	}
}

func TestRunnerResolveCancellationAndTimeout(t *testing.T) {
	binaryDir := buildFakeTerragrunt(t)
	t.Setenv("PATH", binaryDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TG_TEST_MODE", "sleep")
	sourcePath, _, st := dependencyFixture(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := dependency.NewRunner(time.Second).Resolve(ctx, sourcePath, "app", st)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))

	_, err = dependency.NewRunner(10*time.Millisecond).Resolve(t.Context(), sourcePath, "app", st)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
}

func buildFakeTerragrunt(t *testing.T) string {
	t.Helper()
	binaryDir := t.TempDir()
	binaryName := "terragrunt"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(binaryDir, binaryName)
	cmd := exec.Command("go", "build", "-o", binaryPath, "./testdata/fake-terragrunt")
	cmd.Dir = "."
	combined, err := cmd.CombinedOutput()
	require.NoError(t, err, string(combined))
	return binaryDir
}

func dependencyFixture(t *testing.T) (string, string, store.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	sourceDir := filepath.Join(tmpDir, "unit")
	targetDir := filepath.Join(tmpDir, "app")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(targetDir, 0o755))
	sourcePath := filepath.Join(sourceDir, "terragrunt.hcl")
	targetPath := filepath.Join(targetDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(sourcePath, nil, 0o600))
	require.NoError(t, os.WriteFile(targetPath, nil, 0o600))
	st := store.Store{Cfg: &config.TerragruntConfig{TerragruntDependencies: config.Dependencies{{
		Name: "app", ConfigPath: cty.StringVal("../app"),
	}}}}
	return sourcePath, targetPath, st
}

func readFile(t *testing.T, filename string) string {
	t.Helper()
	contents, err := os.ReadFile(filename)
	require.NoError(t, err)
	return string(contents)
}
