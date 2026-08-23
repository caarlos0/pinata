package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/stretchr/testify/require"
)

func TestProcess(t *testing.T) {
	for _, name := range []string{"ci.yml", "simple.yml"} {
		t.Run(name, func(t *testing.T) {
			inPath := filepath.Join("testdata", name)
			outPath := filepath.Join(t.TempDir(), name)

			changed, err := process(inPath, outPath, nil)
			require.NoError(t, err)
			require.True(t, changed, inPath)

			got, err := os.ReadFile(outPath)
			require.NoError(t, err)
			golden.RequireEqual(t, string(got))
		})
	}
}

func TestProcessIgnore(t *testing.T) {
	inPath := filepath.Join("testdata", "ci.yml")
	outPath := filepath.Join(t.TempDir(), "ci.yml")

	changed, err := process(inPath, outPath, globs{"*/*"})
	require.NoError(t, err)
	require.False(t, changed)
	require.NoFileExists(t, outPath)
}

func TestReplaceInLineIgnore(t *testing.T) {
	for name, tt := range map[string]struct {
		line   string
		ignore globs
	}{
		"exact":           {"      - uses: actions/checkout@v4", globs{"actions/checkout"}},
		"org":             {"      - uses: actions/checkout@v4", globs{"actions/*"}},
		"subaction org":   {"      - uses: github/codeql-action/analyze@v2", globs{"github/*"}},
		"subaction exact": {"      - uses: github/codeql-action/analyze@v2", globs{"github/codeql-action/analyze"}},
		"everything":      {"      - uses: actions/checkout@v4 # comment", globs{"*/*"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := replaceInLine(tt.line, tt.ignore)
			require.NoError(t, err)
			require.Equal(t, tt.line, got)
		})
	}
}

func TestGlobs(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		var g globs
		require.NoError(t, g.Set("actions/*"))
		require.NoError(t, g.Set("github/codeql-action"))
		require.Equal(t, globs{"actions/*", "github/codeql-action"}, g)
		require.Equal(t, "actions/*,github/codeql-action", g.String())
	})

	t.Run("invalid", func(t *testing.T) {
		var g globs
		require.Error(t, g.Set("actions/["))
		require.Empty(t, g)
	})

	t.Run("match", func(t *testing.T) {
		require.False(t, globs(nil).match("actions/checkout"))
		require.False(t, globs{"actions/*"}.match("docker/login-action"))
		require.False(t, globs{"actions"}.match("actions/checkout"))
		require.True(t, globs{"docker/*", "actions/*"}.match("actions/checkout"))
	})
}
