package path_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"terragrunt-ls/internal/tg/path"
	"terragrunt-ls/internal/tg/store"

	"github.com/gruntwork-io/terragrunt/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

func TestDependencyConfigUsesEvaluatedTerragruntValue(t *testing.T) {
	t.Parallel()

	st := store.Store{Cfg: &config.TerragruntConfig{
		TerragruntDependencies: config.Dependencies{{
			Name:       "app",
			ConfigPath: cty.StringVal("../app"),
		}},
	}}

	configPath, err := path.DependencyConfig(st, "app")

	require.NoError(t, err)
	assert.Equal(t, "../app", configPath)
}

func TestDependencyTargetResolvesFilesAndCanonicalDirectoryConfigs(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sourceFile := filepath.Join(tmpDir, "unit", "terragrunt.hcl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourceFile), 0o755))
	require.NoError(t, os.WriteFile(sourceFile, nil, 0o600))

	appDir := filepath.Join(tmpDir, "app")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	appConfig := filepath.Join(appDir, "terragrunt.hcl")
	require.NoError(t, os.WriteFile(appConfig, nil, 0o600))

	resolved, err := path.DependencyTarget(sourceFile, "../app")
	require.NoError(t, err)
	assert.Equal(t, appConfig, resolved)

	dataFile := filepath.Join(tmpDir, "data.json")
	require.NoError(t, os.WriteFile(dataFile, []byte("{}"), 0o600))
	resolved, err = path.DependencyTarget(sourceFile, "../data.json")
	require.NoError(t, err)
	assert.Equal(t, dataFile, resolved)
}

func TestDependencyTargetReturnsTypedErrorForMissingTarget(t *testing.T) {
	t.Parallel()

	_, err := path.DependencyTarget(filepath.Join(t.TempDir(), "terragrunt.hcl"), "missing")
	require.Error(t, err)

	var resolutionError *path.ResolutionError
	assert.ErrorAs(t, err, &resolutionError)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}
