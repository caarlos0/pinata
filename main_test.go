package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/exp/golden"
	"github.com/stretchr/testify/require"
)

func TestProcessRealWorkflows(t *testing.T) {
	for _, name := range []string{"ci.yml", "simple.yml"} {
		t.Run(name, func(t *testing.T) {
			inPath := filepath.Join("testdata", name)
			outPath := filepath.Join(t.TempDir(), name)

			changed, err := process(inPath, outPath)
			require.NoError(t, err)
			require.True(t, changed)

			got, err := os.ReadFile(outPath)
			require.NoError(t, err)
			golden.RequireEqual(t, string(got))
		})
	}
}
