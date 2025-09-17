package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func withStubbedHTTP(t *testing.T, fn func(*http.Request) (*http.Response, error)) func() {
	old := httpClient.Transport
	httpClient.Transport = rtFunc(fn)
	return func() { httpClient.Transport = old }
}

func resetGlobals() { cache = map[string]string{} }

func shaOf(ch byte) string { return strings.Repeat(string(ch), 40) }

func TestProcess_WorkflowPinsUsesAndPreservesFormatting(t *testing.T) {
	defer resetGlobals()
	shaMap := map[string]string{
		"actions/checkout@v4":  shaOf('a'),
		"acme/foo@v2":          shaOf('b'),
		"owner/with-single@v1": shaOf('c'),
		"owner/with-quotes@v1": shaOf('d'),
		"owner/repo@v1":        shaOf('e'),
	}
	calls := 0
	defer withStubbedHTTP(t, func(r *http.Request) (*http.Response, error) {
		calls++
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 6 { // /repos/{owner}/{repo}/commits/{ref}
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		owner, repo, ref := parts[2], parts[3], parts[5]
		sha := shaMap[owner+"/"+repo+"@"+ref]
		if sha == "" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		body := `{"sha":"` + sha + `"}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})()

	in := strings.TrimLeft(`name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4   # keep
      - name: Use foo
        uses: acme/foo/sub/act@v2
      - uses: "./ignored/local"
      - uses: 'owner/with-single@v1'
      - uses: "owner/with-quotes@v1"
      - run: echo hello
  call:
    uses: owner/repo/.github/workflows/reusable.yml@v1   # job-level
`, "\n") + "\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yml")
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := process(path)
	assert.NoError(t, err)
	assert.True(t, changed)

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)

	expected := strings.TrimLeft(`name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@`+shaMap["actions/checkout@v4"]+`   # keep
      - name: Use foo
        uses: acme/foo/sub/act@`+shaMap["acme/foo@v2"]+`
      - uses: "./ignored/local"
      - uses: 'owner/with-single@`+shaMap["owner/with-single@v1"]+`'
      - uses: "owner/with-quotes@`+shaMap["owner/with-quotes@v1"]+`"
      - run: echo hello
  call:
    uses: owner/repo/.github/workflows/reusable.yml@`+shaMap["owner/repo@v1"]+`   # job-level
`, "\n") + "\n"
	assert.Equal(t, expected, out)
	// 5 unique owner/repo@ref calls
	assert.Equal(t, 5, calls)
}

func TestProcess_WorkflowSkipsLocalURLAndExpressions(t *testing.T) {
	defer resetGlobals()
	calls := 0
	defer withStubbedHTTP(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
	})()

	in := strings.TrimLeft(`name: SkipCases
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: ./local/action
      - uses: ../local/action@v1
      - uses: /abs/local/action
      - uses: docker://alpine:latest
      - uses: https://example.com/foo@v1
      - uses: acme/foo@\${{ github.ref }}
      - run: echo done
`, "\n") + "\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yml")
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := process(path)
	assert.NoError(t, err)
	assert.False(t, changed)

	b, _ := os.ReadFile(path)
	assert.Equal(t, in, string(b))
	assert.Equal(t, 0, calls)
}

func TestProcess_CacheAcrossWorkflow(t *testing.T) {
	defer resetGlobals()
	sha := shaOf('z')
	calls := 0
	defer withStubbedHTTP(t, func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"sha":"` + sha + `"}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})()

	in := strings.TrimLeft(`name: Cache
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: org/repo@v1
      - uses: org/repo@v1
      - uses: org/repo/path/here@v1
      - uses: org/repo@v1	# again
`, "\n") + "\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.yml")
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := process(path)
	assert.NoError(t, err)
	assert.True(t, changed)

	b, _ := os.ReadFile(path)
	out := string(b)
	expected := strings.TrimLeft(`name: Cache
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: org/repo@`+sha+`
      - uses: org/repo@`+sha+`
      - uses: org/repo/path/here@v1
      - uses: org/repo@`+sha+`	# again
`, "\n") + "\n"
	assert.Equal(t, expected, out)
	assert.Equal(t, 1, calls) // only one unique owner/repo@ref
}
