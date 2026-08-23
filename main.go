package main

import (
	"bufio"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/caarlos0/log"
)

const (
	usesPrefix = "uses:"
	shaLen     = 40
)

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	token      = os.Getenv("GITHUB_TOKEN")
	tagCache   = map[string]Tag{}
	refCache   = map[string]Object{}
)

func main() {
	var ignore globs
	flag.Var(&ignore, "ignore", "glob of actions to ignore, may be set multiple times (e.g. github/*)")
	flag.Usage = usage
	flag.Parse()

	dir := cmp.Or(flag.Arg(0), ".github/workflows")
	log.WithField("dir", dir).Info("pinning")
	var changed, total int
	if err := filepath.WalkDir(dir, func(file string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isYaml(file) {
			return nil
		}
		total++
		didChange, err := process(file, file, ignore)
		if err != nil {
			log.WithError(err).
				WithField("file", file).
				Error("could not process")
			return err
		}
		if didChange {
			changed++
			log.WithField("file", file).
				Info("updated")
		}
		return nil
	}); err != nil {
		os.Exit(1)
	}
	log.WithField("dir", dir).
		WithField("total", total).
		WithField("changed", changed).
		Info("done!")
}

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), `pinata pins the GitHub Actions used in [dir] to their SHAs.

Usage: pinata [options] [dir]

Dir defaults to .github/workflows.

Options:
`)
	flag.PrintDefaults()
}

// globs is a list of action globs, e.g. github/*.
type globs []string

func (g *globs) String() string { return strings.Join(*g, ",") }

func (g *globs) Set(s string) error {
	// empty name never matches, but the pattern is still validated.
	if _, err := path.Match(s, ""); err != nil {
		return fmt.Errorf("invalid glob %q: %w", s, err)
	}
	*g = append(*g, s)
	return nil
}

// match reports whether any of the given action names match any of the globs.
func (g globs) match(names ...string) bool {
	for _, glob := range g {
		for _, name := range names {
			if ok, _ := path.Match(glob, name); ok {
				return true
			}
		}
	}
	return false
}

func process(inPath, outPath string, ignore globs) (bool, error) {
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
		newLine, err := replaceInLine(line, ignore)
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

func replaceInLine(line string, ignore globs) (string, error) {
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
		log.WithField("line", line).
			WithField("ref", ref).
			Debug("ignoring")
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

	if ignore.match(repo, baseRepo) {
		log.WithField("line", line).
			WithField("action", repo).
			Debug("ignoring")
		return line, nil
	}

	tagName, newRef, err := getInfo(baseRepo, ref)
	if err != nil {
		return line, err
	}

	line = strings.Replace(line, dep, repo+"@"+newRef, 1)
	if tagName != "" && tagName != newRef {
		// remove any trailing comments
		if idx := strings.Index(line, " # "); idx > -1 {
			line = line[:idx]
		}
		// add the tag comment if we have it
		line += " # " + tagName
	}
	return line, nil
}

func getInfo(baseRepo, version string) (string, string, error) {
	tag, err := getTag(baseRepo, version)
	if err != nil {
		return "", "", err
	}
	ref, err := getRef(baseRepo, version)
	if err != nil {
		return "", "", err
	}
	tagName := cmp.Or(tag.Name, version)
	newRef := cmp.Or(ref.SHA, tag.Object.SHA, version)
	return tagName, newRef, nil
}

func getRef(repo, ref string) (Object, error) {
	key := repo + "@" + ref
	if v, ok := refCache[key]; ok {
		return v, nil
	}
	r, status, err := getGH("https://api.github.com/repos/" + repo + "/commits/" + ref)
	if err != nil {
		return Object{}, fmt.Errorf("github: branch: %s: %w", key, err)
	}
	if status != http.StatusOK {
		return Object{}, fmt.Errorf("github: branch: %s: status %d", key, status)
	}

	var obj Object
	if err := json.NewDecoder(r).Decode(&obj); err != nil {
		return Object{}, fmt.Errorf("github: branch: %s: %w", key, err)
	}
	refCache[key] = obj
	return obj, nil
}

func getTag(repo, ref string) (Tag, error) {
	key := repo + "@" + ref
	if v, ok := tagCache[key]; ok {
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
		v, err := semver.NewVersion(tag.Name)
		if err != nil {
			log.WithField("tag", tag.Name).
				Warn("ignoring invalid tag")
			continue
		}
		tag.Version = v
		if tag.Name == ref {
			ref = tag.Object.SHA
		}
		if tag.Object.SHA == ref {
			candidates = append(candidates, tag)
		}
	}
	slices.SortFunc(candidates, func(a, b Tag) int {
		return a.Version.Compare(b.Version)
	})
	if len(candidates) == 0 {
		return Tag{}, nil
	}
	result := candidates[len(candidates)-1]
	tagCache[key] = result
	return result, nil
}

func isYaml(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yml" || ext == ".yaml"
}

func getGH(url string) (io.ReadCloser, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "pinata")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.StatusCode, nil
}

type Tag struct {
	Ref     string          `json:"ref"`
	Name    string          `json:"-"`
	Version *semver.Version `json:"-"`
	Object  Object          `json:"object"`
}

type Object struct {
	SHA string `json:"sha"`
}

func isSHA(s string) bool {
	if len(s) != shaLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
