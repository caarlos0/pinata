package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/stretchr/testify/require"
)

func TestCLI(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		err := cli([]string{"-h"})
		require.ErrorIs(t, err, flag.ErrHelp)
	})

	t.Run("pin default dir", func(t *testing.T) {
		tmp := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".github", "workflows"), 0o755))
		workflowPath := filepath.Join(tmp, ".github", "workflows", "ci.yml")
		require.NoError(t, os.WriteFile(workflowPath, []byte("name: CI\non: [push]\n"), 0o644))

		wd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(tmp))
		t.Cleanup(func() {
			_ = os.Chdir(wd)
		})

		require.NoError(t, cli(nil))
	})

	t.Run("update flag", func(t *testing.T) {
		require.NoError(t, cli([]string{"-update", t.TempDir()}))
	})

	t.Run("skip-orgs flag", func(t *testing.T) {
		dir := t.TempDir()
		workflowPath := filepath.Join(dir, "ci.yml")
		require.NoError(t, os.WriteFile(workflowPath, []byte("      - uses: actions/checkout@v4\n"), 0o644))
		require.NoError(t, cli([]string{"-skip-orgs", "actions", dir}))

		got, err := os.ReadFile(workflowPath)
		require.NoError(t, err)
		require.Equal(t, "      - uses: actions/checkout@v4\n", string(got))
	})

	t.Run("positional dir", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, cli([]string{dir}))
	})

	t.Run("missing directory", func(t *testing.T) {
		err := cli([]string{t.TempDir() + "/missing"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "directory not found")
	})

	t.Run("rejects extra args", func(t *testing.T) {
		err := cli([]string{".", "extra"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected arguments")
	})
}

func TestProcess(t *testing.T) {
	for _, name := range []string{"ci.yml", "simple.yml"} {
		t.Run(name, func(t *testing.T) {
			resetCaches()
			inPath := filepath.Join("testdata", name)
			outPath := filepath.Join(t.TempDir(), name)

			changed, err := processFile(inPath, outPath, func(line string) (string, error) {
				return replaceInLine(line, nil)
			})
			require.NoError(t, err)
			require.True(t, changed, inPath)

			got, err := os.ReadFile(outPath)
			require.NoError(t, err)
			golden.RequireEqual(t, string(got))
		})
	}
}

func TestParseUsesLine(t *testing.T) {
	tests := []struct {
		line        string
		wantDep     string
		wantRepo    string
		wantRef     string
		wantComment string
		wantOK      bool
	}{
		{
			line:        "      - uses: actions/checkout@v4",
			wantDep:     "actions/checkout@v4",
			wantRepo:    "actions/checkout",
			wantRef:     "v4",
			wantComment: "",
			wantOK:      true,
		},
		{
			line:        "      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5 # v4.3.1",
			wantDep:     "actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5",
			wantRepo:    "actions/checkout",
			wantRef:     "34e114876b0b11c390a56381ad16ebd13914f8d5",
			wantComment: "v4.3.1",
			wantOK:      true,
		},
		{
			line:        "        uses: actions/setup-go@7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4 # v4.3.0",
			wantDep:     "actions/setup-go@7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4",
			wantRepo:    "actions/setup-go",
			wantRef:     "7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4",
			wantComment: "v4.3.0",
			wantOK:      true,
		},
		{
			line:        "      - uses: 'actions/checkout@v4'",
			wantDep:     "actions/checkout@v4",
			wantRepo:    "actions/checkout",
			wantRef:     "v4",
			wantComment: "",
			wantOK:      true,
		},
		{
			line:   "      - name: build",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			dep, repo, ref, comment, ok := parseUsesLine(tt.line)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				return
			}
			require.Equal(t, tt.wantDep, dep)
			require.Equal(t, tt.wantRepo, repo)
			require.Equal(t, tt.wantRef, ref)
			require.Equal(t, tt.wantComment, comment)
		})
	}
}

func TestParseSkipOrgs(t *testing.T) {
	require.Nil(t, parseSkipOrgs(""))
	require.Equal(t, []string{"actions", "microsoft"}, parseSkipOrgs("actions,microsoft"))
	require.Equal(t, []string{"actions", "microsoft"}, parseSkipOrgs(" actions , microsoft "))
}

func TestShouldSkipOrg(t *testing.T) {
	skip := []string{"actions", "microsoft"}
	require.True(t, shouldSkipOrg("actions/checkout", skip))
	require.True(t, shouldSkipOrg("microsoft/nuget-setup@v1", skip))
	require.False(t, shouldSkipOrg("github/codeql-action/autobuild", skip))
	require.False(t, shouldSkipOrg("actions/checkout", nil))
}

func TestActionOrg(t *testing.T) {
	require.Equal(t, "actions", actionOrg("actions/checkout"))
	require.Equal(t, "microsoft", actionOrg("microsoft/nuget-setup"))
}

func TestReplaceInLineSkipOrgs(t *testing.T) {
	line := "      - uses: actions/checkout@v4"
	got, err := replaceInLine(line, []string{"actions"})
	require.NoError(t, err)
	require.Equal(t, line, got)
}

func TestShouldSkipUses(t *testing.T) {
	require.True(t, shouldSkipUses("./local/action", "v1"))
	require.True(t, shouldSkipUses("docker://alpine", "3.18"))
	require.True(t, shouldSkipUses("actions/checkout", "${{ matrix.ref }}"))
	require.False(t, shouldSkipUses("actions/checkout", "v4"))
}

func TestBaseRepo(t *testing.T) {
	require.Equal(t, "actions/checkout", baseRepo("actions/checkout"))
	require.Equal(t, "github/codeql-action", baseRepo("github/codeql-action/autobuild"))
}

func TestUpdateInLineWith(t *testing.T) {
	currentSHA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	line := "      - uses: actions/checkout@" + currentSHA + " # v4.3.1"

	t.Run("skips version refs", func(t *testing.T) {
		got, err := updateInLineWith("      - uses: actions/checkout@v4", nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "      - uses: actions/checkout@v4", got)
	})

	t.Run("skips when already at latest version", func(t *testing.T) {
		latest := Tag{
			Name:    "v4.3.1",
			Version: semver.MustParse("4.3.1"),
		}
		got, err := updateInLineWith(line, nil, func(string) (Tag, error) {
			return latest, nil
		}, nil)
		require.NoError(t, err)
		require.Equal(t, line, got)
	})

	t.Run("skips when latest resolves to same sha", func(t *testing.T) {
		latest := Tag{
			Name:    "v4.4.0",
			Version: semver.MustParse("4.4.0"),
		}
		got, err := updateInLineWith(line, nil, func(string) (Tag, error) {
			return latest, nil
		}, func(string, string) (string, string, error) {
			return "v4.4.0", currentSHA, nil
		})
		require.NoError(t, err)
		require.Equal(t, line, got)
	})

	t.Run("updates sha and comment", func(t *testing.T) {
		latest := Tag{
			Name:    "v5.0.0",
			Version: semver.MustParse("5.0.0"),
		}
		got, err := updateInLineWith(line, nil, func(string) (Tag, error) {
			return latest, nil
		}, func(string, string) (string, string, error) {
			return "v5.0.0", newSHA, nil
		})
		require.NoError(t, err)
		require.Equal(t, "      - uses: actions/checkout@"+newSHA+" # v5.0.0", got)
	})

	t.Run("replaces compact comment delimiter", func(t *testing.T) {
		compactCommentLine := "      - uses: actions/checkout@" + currentSHA + " #v4.3.1"
		latest := Tag{
			Name:    "v5.0.0",
			Version: semver.MustParse("5.0.0"),
		}
		got, err := updateInLineWith(compactCommentLine, nil, func(string) (Tag, error) {
			return latest, nil
		}, func(string, string) (string, string, error) {
			return "v5.0.0", newSHA, nil
		})
		require.NoError(t, err)
		require.Equal(t, "      - uses: actions/checkout@"+newSHA+" # v5.0.0", got)
	})

	t.Run("updates sha without comment when semver comment is invalid", func(t *testing.T) {
		line := "      - uses: github/codeql-action/analyze@" + currentSHA + " # v2"
		latest := Tag{
			Name:    "v3.30.3",
			Version: semver.MustParse("3.30.3"),
		}
		got, err := updateInLineWith(line, nil, func(string) (Tag, error) {
			return latest, nil
		}, func(string, string) (string, string, error) {
			return "v3.30.3", newSHA, nil
		})
		require.NoError(t, err)
		require.Equal(t, "      - uses: github/codeql-action/analyze@"+newSHA+" # v3.30.3", got)
	})

	t.Run("skips local and docker uses", func(t *testing.T) {
		local := "      - uses: ./local/action@" + currentSHA + " # v1.0.0"
		got, err := updateInLineWith(local, nil, func(string) (Tag, error) {
			t.Fatal("should not fetch tags for local actions")
			return Tag{}, nil
		}, nil)
		require.NoError(t, err)
		require.Equal(t, local, got)
	})
}
