package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usesPrefix = "uses:"

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	token      = os.Getenv("GITHUB_TOKEN")
	cache      = map[string]Tag{}
)

func main() {
	dir := ".github/workflows"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	var changed, total int
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isYaml(path) {
			return nil
		}
		total++
		didChange, err := process(path, path)
		if err != nil {
			fmt.Fprintln(os.Stderr, path+": "+err.Error())
			return err
		}
		if didChange {
			changed++
			fmt.Println("updated", path)
		}
		return nil
	}); err != nil {
		os.Exit(1)
	}
	fmt.Printf("changed %d out of %d files\n", changed, total)
}

func process(inPath, outPath string) (bool, error) {
	f, err := os.Open(inPath)
	if err != nil {
		return false, fmt.Errorf("process: %w", err)
	}
	defer f.Close() //nolint:errcheck

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
			return false, fmt.Errorf("process: %w", err)
		}
		changed = changed || newLine != line
		out.WriteString(newLine)
		out.WriteByte('\n')
	}
	if err := s.Err(); err != nil {
		return false, fmt.Errorf("process: %w", err)
	}
	if changed {
		if err := os.WriteFile(outPath, []byte(out.String()), 0o644); err != nil {
			return false, fmt.Errorf("process: %w", err)
		}
	}
	return changed, nil
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

	baseRepo := repo
	if parts := strings.SplitN(repo, "/", 3); len(parts) >= 3 {
		baseRepo = parts[0] + "/" + parts[1]
	}

	obj, err := getFullTag(baseRepo, ref)
	if err != nil {
		return line, err
	}
	if obj.Object.SHA == "" {
		// its not a tag, so maybe pointing to a branch?
		obj, err = getBranch(baseRepo, ref)
		if err != nil {
			return line, err
		}
	}
	tag := obj.Name
	ref = obj.Object.SHA

	line = strings.Replace(line, dep, repo+"@"+ref, 1)
	// remove any trailing comments
	if idx := strings.Index(line, " # "); idx > -1 {
		line = line[:idx]
	}
	// add the tag comment if we have it
	if tag != "" {
		line += " # " + tag
	}
	return line, nil
}

func getBranch(repo, ref string) (Tag, error) {
	key := repo + "@" + ref
	if v, ok := cache[key]; ok {
		return v, nil
	}
	r, status, err := getGH("https://api.github.com/repos/" + repo + "/commits/" + ref)
	if err != nil {
		return Tag{}, fmt.Errorf("github: branch: %s: %w", key, err)
	}
	if status != http.StatusOK {
		return Tag{}, fmt.Errorf("github: branch: %s: status %d", key, status)
	}

	var obj Object
	if err := json.NewDecoder(r).Decode(&obj); err != nil {
		return Tag{}, fmt.Errorf("github: branch: %s: %w", key, err)
	}
	result := Tag{
		Name:   ref,
		Object: obj,
	}
	cache[key] = result
	return result, nil
}

func getFullTag(repo, ref string) (Tag, error) {
	key := repo + "@" + ref
	if v, ok := cache[key]; ok {
		return v, nil
	}
	r, status, err := getGH("https://api.github.com/repos/" + repo + "/git/refs/tags")
	if err != nil {
		return Tag{}, fmt.Errorf("github: tag: %s: %w", key, err)
	}
	defer r.Close() // nolint:errcheck
	if status == http.StatusNotFound {
		return Tag{}, nil
	}
	var out []Tag
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return Tag{}, fmt.Errorf("github: tag: %s: %w", key, err)
	}
	var candidates []Tag
	for _, tag := range out {
		tag.Name = strings.TrimPrefix(tag.Ref, "refs/tags/")
		if tag.Name == ref {
			ref = tag.Object.SHA
		}
		if tag.Object.SHA == ref {
			candidates = append(candidates, tag)
		}
	}

	if len(candidates) == 0 {
		return Tag{}, nil
	}
	result := candidates[len(candidates)-1]
	cache[key] = result
	return result, nil
}

func isYaml(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yml" || ext == ".yaml"
}

func getGH(url string) (io.ReadCloser, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 500, err
	}
	req.Header.Set("User-Agent", "pinata")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 500, err
	}
	return resp.Body, resp.StatusCode, nil
}

type Tag struct {
	Ref    string `json:"ref"`
	Name   string `json:"-"`
	Object Object `json:"object"`
}

type Object struct {
	SHA string `json:"sha"`
}
