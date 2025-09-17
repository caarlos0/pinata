package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	usesPrefix = "uses:"
	shaLen     = 40
)

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	token      = os.Getenv("GITHUB_TOKEN")
	cache      = map[string]string{}
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".yml" {
			return nil
		}
		changed, err := process(path, path)
		if err != nil {
			fmt.Fprintln(os.Stderr, path+": "+err.Error())
			return err
		}
		if changed {
			fmt.Println("updated", path)
		}
		return nil
	}); err != nil {
		os.Exit(1)
	}
}

func process(inPath, outPath string) (bool, error) {
	f, err := os.Open(inPath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var out strings.Builder
	s := bufio.NewScanner(f)
	changed := false
	for s.Scan() {
		line := s.Text()
		if !strings.Contains(line, usesPrefix) {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		newLine, err := replaceInLine(line)
		if err != nil {
			return false, err
		}
		changed = changed || newLine != line
		out.WriteString(newLine)
		out.WriteByte('\n')
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	return changed, os.WriteFile(outPath, []byte(out.String()), 0o644)
}

func replaceInLine(line string) (string, error) {
	dep := line
	if i := strings.Index(dep, usesPrefix); i >= 0 {
		dep = dep[i+len(usesPrefix):]
	}
	if i := strings.Index(dep, " #"); i >= 0 {
		dep = dep[:i]
	}
	dep = strings.TrimSpace(dep)
	dep = strings.TrimFunc(dep, func(r rune) bool {
		return r == '"' || r == '\''
	})
	if dep == line {
		return line, nil
	}

	repo, ref, ok := strings.Cut(dep, "@")
	if !ok {
		return line, nil
	}

	if isSHA(ref) {
		return line, nil
	}

	// skip local paths, docker, urls, and expressions
	if strings.HasPrefix(repo, "./") ||
		strings.HasPrefix(repo, "../") ||
		strings.HasPrefix(repo, "/") ||
		strings.HasPrefix(repo, "docker://") ||
		strings.HasPrefix(repo, "http://") ||
		strings.HasPrefix(repo, "https://") ||
		strings.Contains(ref, "${{") {
		return line, nil
	}

	// handle subpaths: only pin if it's a reusable workflow under .github/workflows/
	baseRepo := repo
	if parts := strings.SplitN(repo, "/", 3); len(parts) >= 3 {
		if !strings.HasPrefix(parts[2], ".github/workflows/") {
			return line, nil
		}
		baseRepo = parts[0] + "/" + parts[1]
	}

	newRef, err := resolve(baseRepo, ref)
	if err != nil {
		return line, err
	}

	return strings.Replace(line, dep, repo+"@"+newRef, 1), nil
}

func isSHA(s string) bool {
	if len(s) != shaLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func resolve(repo, ref string) (string, error) {
	key := repo + "@" + ref
	if v, ok := cache[key]; ok {
		return v, nil
	}
	url := "https://api.github.com/repos/" + repo + "/commits/" + ref
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: %s: %w", key, err)
	}
	req.Header.Set("User-Agent", "gh-action-hasher")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("github: %s: %s: %s", key, resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("github: %s: %w", key, err)
	}
	if out.SHA == "" {
		return "", fmt.Errorf("empty sha")
		return "", fmt.Errorf("github: empty sha")
	}
	cache[key] = out.SHA
	return out.SHA, nil
}
