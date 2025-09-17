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
	"regexp"
	"strings"
	"time"
)

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	token      = os.Getenv("GITHUB_TOKEN")
	cache      = map[string]string{}
	// Two simple patterns: with extra path segment(s) and without
	reUseWithPath = regexp.MustCompile(`([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)/([A-Za-z0-9/_.-]+)@v[0-9][A-Za-z0-9._-]*`)
	reUseNoPath   = regexp.MustCompile(`([A-Za-z0-9-]+)/([A-Za-z0-9._-]+)@v[0-9][A-Za-z0-9._-]*`)
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".yml") {
			return nil
		}
		changed, err := process(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, path+": "+err.Error())
			os.Exit(1)
			return nil
		}
		if changed {
			fmt.Println("updated", path)
		}
		return nil
	})
}

func process(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var out strings.Builder
	s := bufio.NewScanner(f)
	changed := false
	for s.Scan() {
		line := s.Text()
		if strings.Contains(line, "uses:") {
			newLine := replaceInLine(line)
			if newLine != line {
				changed = true
			}
			out.WriteString(newLine)
			out.WriteByte('\n')
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func replaceInLine(line string) string {
	idxHash := strings.IndexByte(line, '#')
	comment := ""
	if idxHash >= 0 {
		comment = line[idxHash:]
		line = line[:idxHash]
	}
	orig := line
	if !strings.Contains(line, "uses:") {
		return orig + comment
	}
	// Replace within the whole (non-comment) line; patterns only match owner/repo@v*
	line = reUseWithPath.ReplaceAllStringFunc(line, func(m string) string {
		left, ref, ok := splitAt(m, "@")
		if !ok {
			return m
		}
		parts := strings.Split(left, "/")
		if len(parts) < 3 { // owner/repo/path...
			return m
		}
		owner, repo := parts[0], parts[1]
		if isSHA(ref) {
			return m
		}
		sha, err := resolve(owner, repo, ref)
		if err != nil || sha == "" {
			return m
		}
		return left + "@" + sha
	})
	line = reUseNoPath.ReplaceAllStringFunc(line, func(m string) string {
		left, ref, ok := splitAt(m, "@")
		if !ok {
			return m
		}
		parts := strings.Split(left, "/")
		if len(parts) != 2 {
			return m
		}
		owner, repo := parts[0], parts[1]
		if isSHA(ref) {
			return m
		}
		sha, err := resolve(owner, repo, ref)
		if err != nil || sha == "" {
			return m
		}
		return owner + "/" + repo + "@" + sha
	})
	if line == orig {
		return orig + comment
	}
	return line + comment
}


func splitAt(s, sep string) (string, string, bool) {
	p := strings.SplitN(s, sep, 2)
	if len(p) != 2 {
		return s, "", false
	}
	return p[0], p[1], true
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func resolve(owner, repo, ref string) (string, error) {
	key := owner + "/" + repo + "@" + ref
	if v, ok := cache[key]; ok {
		return v, nil
	}
	url := "https://api.github.com/repos/" + owner + "/" + repo + "/commits/" + ref
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gh-action-hasher")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("github api error: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.SHA == "" {
		return "", fmt.Errorf("empty sha")
	}
	cache[key] = out.SHA
	return out.SHA, nil
}
