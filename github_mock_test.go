package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type mockTag struct {
	name string
	sha  string
}

type gitHubFixture struct {
	commits map[string]string
	tags    []mockTag
}

var githubFixtures = map[string]gitHubFixture{
	"actions/checkout": {
		commits: map[string]string{
			"v4":     "34e114876b0b11c390a56381ad16ebd13914f8d5",
			"v4.3.1": "34e114876b0b11c390a56381ad16ebd13914f8d5",
		},
		tags: []mockTag{
			{name: "v4", sha: "34e114876b0b11c390a56381ad16ebd13914f8d5"},
			{name: "v4.3.1", sha: "34e114876b0b11c390a56381ad16ebd13914f8d5"},
		},
	},
	"actions/setup-go": {
		commits: map[string]string{
			"v4":     "7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4",
			"v4.3.0": "7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4",
		},
		tags: []mockTag{
			{name: "v4", sha: "7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4"},
			{name: "v4.3.0", sha: "7b8cf10d4e4a01d4992d18a89f4d7dc5a3e6d6f4"},
		},
	},
	"caarlos0/meta": {
		commits: map[string]string{
			"v0.1.0": "c7f17af352dac91fa6c785d06ebac8547f1abdd3",
		},
		tags: []mockTag{
			{name: "v0.1.0", sha: "c7f17af352dac91fa6c785d06ebac8547f1abdd3"},
		},
	},
	"docker/login-action": {
		commits: map[string]string{
			"v2":     "465a07811f14bebb1938fbed4728c6a1ff8901fc",
			"v2.2.0": "465a07811f14bebb1938fbed4728c6a1ff8901fc",
		},
		tags: []mockTag{
			{name: "v2", sha: "465a07811f14bebb1938fbed4728c6a1ff8901fc"},
			{name: "v2.2.0", sha: "465a07811f14bebb1938fbed4728c6a1ff8901fc"},
		},
	},
	"docker/setup-qemu-action": {
		commits: map[string]string{
			"v2":     "2b82ce82d56a2a04d2637cd93a637ae1b359c0a7",
			"v2.2.0": "2b82ce82d56a2a04d2637cd93a637ae1b359c0a7",
		},
		tags: []mockTag{
			{name: "v2", sha: "2b82ce82d56a2a04d2637cd93a637ae1b359c0a7"},
			{name: "v2.2.0", sha: "2b82ce82d56a2a04d2637cd93a637ae1b359c0a7"},
		},
	},
	"docker/setup-buildx-action": {
		commits: map[string]string{
			"v2":      "885d1462b80bc1c1c7f0b00334ad271f09369c55",
			"v2.10.0": "885d1462b80bc1c1c7f0b00334ad271f09369c55",
		},
		tags: []mockTag{
			{name: "v2", sha: "885d1462b80bc1c1c7f0b00334ad271f09369c55"},
			{name: "v2.10.0", sha: "885d1462b80bc1c1c7f0b00334ad271f09369c55"},
		},
	},
	"github/codeql-action": {
		commits: map[string]string{
			"v2": "b8d3b6e8af63cde30bdc382c0bc28114f4346c88",
		},
	},
	"ossf/scorecard-action": {
		commits: map[string]string{
			"v2.4.2": "05b42c624433fc40578a4040d5cf5e36ddca8cde",
		},
		tags: []mockTag{
			{name: "v2.4.2", sha: "05b42c624433fc40578a4040d5cf5e36ddca8cde"},
		},
	},
}

func TestMain(m *testing.M) {
	server := httptest.NewServer(http.HandlerFunc(mockGitHub))
	apiBaseURL = server.URL
	code := m.Run()
	server.Close()
	os.Exit(code)
}

func mockGitHub(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/git/refs/tags"):
		repo := strings.TrimPrefix(path, "/repos/")
		repo = strings.TrimSuffix(repo, "/git/refs/tags")
		serveTags(w, r, repo)
	case strings.Contains(path, "/commits/"):
		repo, ref, ok := strings.Cut(strings.TrimPrefix(path, "/repos/"), "/commits/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveCommit(w, r, repo, ref)
	default:
		http.NotFound(w, r)
	}
}

func fixtureFor(repo string) (gitHubFixture, bool) {
	f, ok := githubFixtures[repo]
	return f, ok
}

func serveTags(w http.ResponseWriter, r *http.Request, repo string) {
	fixture, ok := fixtureFor(repo)
	if !ok {
		http.NotFound(w, r)
		return
	}
	out := make([]Tag, 0, len(fixture.tags))
	for _, tag := range fixture.tags {
		out = append(out, Tag{
			Ref:    "refs/tags/" + tag.name,
			Object: Object{SHA: tag.sha},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func serveCommit(w http.ResponseWriter, r *http.Request, repo, ref string) {
	fixture, ok := fixtureFor(repo)
	if !ok {
		http.NotFound(w, r)
		return
	}
	sha, ok := fixture.commits[ref]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Object{SHA: sha})
}
