package main

import (
	"bufio"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/caarlos0/log"
)

const (
	usesPrefix          = "uses:"
	shaLen              = 40
	defaultWorkflowsDir = ".github/workflows"
)

var (
	httpClient    = &http.Client{Timeout: 15 * time.Second}
	token         = os.Getenv("GITHUB_TOKEN")
	apiBaseURL    = "https://api.github.com"
	refCache      = map[string]Object{}
	repoTagsCache = map[string][]Tag{}
)

func main() {
	if err := cli(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(1)
	}
}

func cli(args []string) error {
	fs := flag.NewFlagSet("pinata", flag.ContinueOnError)
	fs.Usage = func() { printUsage(fs) }

	update := fs.Bool("update", false, "bump pinned actions to the latest release")
	skipOrgsFlag := fs.String("skip-orgs", "", "comma-separated action org prefixes to skip (e.g. actions,microsoft)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := defaultWorkflowsDir
	switch rest := fs.Args(); len(rest) {
	case 0:
	case 1:
		dir = rest[0]
	default:
		return fmt.Errorf("unexpected arguments: %v", rest[1:])
	}

	if err := validateDir(dir); err != nil {
		return err
	}

	return run(*update, dir, parseSkipOrgs(*skipOrgsFlag))
}

func parseSkipOrgs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func validateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory not found: %s", dir)
		}
		return fmt.Errorf("directory: %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", dir)
	}
	return nil
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, "Usage: pinata [flags] [dir]\n\n")
	fmt.Fprintf(os.Stderr, "Pin GitHub Actions to their commit SHAs, or update pinned actions to the latest release.\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  pinata\n")
	fmt.Fprintf(os.Stderr, "  pinata ./myrepo/.github/workflows\n")
	fmt.Fprintf(os.Stderr, "  pinata -update\n")
	fmt.Fprintf(os.Stderr, "  pinata -skip-orgs actions,microsoft\n\n")
	fmt.Fprintf(os.Stderr, "dir defaults to %s\n\n", defaultWorkflowsDir)
	fmt.Fprintf(os.Stderr, "Flags:\n")
	fmt.Fprintf(os.Stderr, "-h, -help    show this help message and exit\n")
	fs.PrintDefaults()
}

func run(update bool, dir string, skipOrgs []string) error {
	logVerb := "pinning"
	if update {
		logVerb = "updating"
	}
	log.WithField("dir", dir).Info(logVerb)

	var fn func(string) (string, error)
	if update {
		fn = func(line string) (string, error) { return updateInLine(line, skipOrgs) }
	} else {
		fn = func(line string) (string, error) { return replaceInLine(line, skipOrgs) }
	}

	var changed, total int
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isYaml(path) {
			return nil
		}
		total++
		didChange, err := processFile(path, path, fn)
		if err != nil {
			log.WithError(err).
				WithField("file", path).
				Error("could not process")
			return err
		}
		if didChange {
			changed++
			log.WithField("file", path).
				Info("updated")
		}
		return nil
	}); err != nil {
		return err
	}
	log.WithField("dir", dir).
		WithField("total", total).
		WithField("changed", changed).
		Info("done!")
	return nil
}

func processFile(inPath, outPath string, fn func(string) (string, error)) (bool, error) {
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
		newLine, err := fn(line)
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

type usesLine struct {
	dep, repo, ref, comment string
}

func parseUsesLine(line string) (dep, repo, ref, commentVersion string, ok bool) {
	idx := strings.Index(line, usesPrefix)
	if idx < 0 {
		return "", "", "", "", false
	}
	dep = line[idx+len(usesPrefix):]
	if i := strings.Index(dep, " #"); i >= 0 {
		commentVersion = strings.TrimSpace(dep[i+2:])
		dep = dep[:i]
	}
	dep = strings.TrimSpace(dep)
	dep = strings.TrimFunc(dep, func(r rune) bool {
		return r == '"' || r == '\''
	})
	repo, ref, ok = strings.Cut(dep, "@")
	if !ok {
		return dep, "", "", commentVersion, false
	}
	return dep, repo, ref, commentVersion, true
}

func parseActionUses(line string, skipOrgs []string) (usesLine, bool) {
	dep, repo, ref, comment, ok := parseUsesLine(line)
	if !ok || shouldSkipUses(repo, ref) || shouldSkipOrg(repo, skipOrgs) {
		return usesLine{}, false
	}
	return usesLine{dep, repo, ref, comment}, true
}

func actionOrg(repo string) string {
	org, _, _ := strings.Cut(repo, "/")
	return org
}

func shouldSkipOrg(repo string, skipOrgs []string) bool {
	if len(skipOrgs) == 0 {
		return false
	}
	org := actionOrg(repo)
	return slices.Contains(skipOrgs, org)
}

func shouldSkipUses(repo, ref string) bool {
	// skip local paths, docker, urls, and expressions
	return strings.HasPrefix(repo, "./") ||
		strings.HasPrefix(repo, "../") ||
		strings.HasPrefix(repo, "/") ||
		strings.HasPrefix(repo, "docker://") ||
		strings.HasPrefix(repo, "http://") ||
		strings.HasPrefix(repo, "https://") ||
		strings.Contains(ref, "${{")
}

func baseRepo(repo string) string {
	if parts := strings.SplitN(repo, "/", 3); len(parts) >= 3 {
		return parts[0] + "/" + parts[1]
	}
	return repo
}

func rewriteUsesLine(line, dep, repo, newRef, tagName string) string {
	line = strings.Replace(line, dep, repo+"@"+newRef, 1)
	if tagName != "" && tagName != newRef {
		// remove any trailing comments
		if idx := strings.Index(line, " #"); idx > -1 {
			line = line[:idx]
		}

		// add the tag comment if we have it
		line += " # " + tagName
	}
	return line
}

func replaceInLine(line string, skipOrgs []string) (string, error) {
	uses, ok := parseActionUses(line, skipOrgs)
	if !ok {
		return line, nil
	}

	if isSHA(uses.ref) {
		log.WithField("line", line).
			WithField("ref", uses.ref).
			Debug("ignoring")
		return line, nil
	}

	tagName, newRef, err := getInfo(baseRepo(uses.repo), uses.ref)
	if err != nil {
		return line, err
	}

	return rewriteUsesLine(line, uses.dep, uses.repo, newRef, tagName), nil
}

func updateInLine(line string, skipOrgs []string) (string, error) {
	return updateInLineWith(line, skipOrgs, getLatestTag, getInfo)
}

type (
	tagGetter  func(string) (Tag, error)
	infoGetter func(string, string) (string, string, error)
)

func updateInLineWith(line string, skipOrgs []string, latest tagGetter, info infoGetter) (string, error) {
	uses, ok := parseActionUses(line, skipOrgs)
	if !ok || !isSHA(uses.ref) {
		return line, nil
	}

	latestTag, err := latest(baseRepo(uses.repo))
	if err != nil {
		return line, err
	}
	if latestTag.Name == "" {
		return line, nil
	}

	if uses.comment != "" {
		if cur, err := semver.NewVersion(strings.TrimPrefix(uses.comment, "v")); err == nil {
			if !latestTag.Version.GreaterThan(cur) {
				return line, nil
			}
		}
	}

	tagName, newRef, err := info(baseRepo(uses.repo), latestTag.Name)
	if err != nil {
		return line, err
	}
	if newRef == uses.ref {
		return line, nil
	}

	return rewriteUsesLine(line, uses.dep, uses.repo, newRef, tagName), nil
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
	obj, status, err := githubJSON[Object](apiBaseURL + "/repos/" + repo + "/commits/" + ref)
	if err != nil {
		return Object{}, fmt.Errorf("github: branch: %s: %w", key, err)
	}
	if status != http.StatusOK {
		return Object{}, fmt.Errorf("github: branch: %s: status %d", key, status)
	}
	refCache[key] = obj
	return obj, nil
}

func listSemverTags(repo string) ([]Tag, error) {
	if v, ok := repoTagsCache[repo]; ok {
		return v, nil
	}
	out, status, err := githubJSON[[]Tag](apiBaseURL + "/repos/" + repo + "/git/refs/tags")
	if err != nil {
		return nil, fmt.Errorf("github: tags: %s: %w", repo, err)
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("github: tags: %s: status %d", repo, status)
	}
	var tags []Tag
	for _, tag := range out {
		tag.Name = strings.TrimPrefix(tag.Ref, "refs/tags/")
		v, err := semver.NewVersion(tag.Name)
		if err != nil {
			log.WithField("tag", tag.Name).
				Warn("ignoring invalid tag")
			continue
		}
		tag.Version = v
		tags = append(tags, tag)
	}
	slices.SortFunc(tags, func(a, b Tag) int {
		return a.Version.Compare(b.Version)
	})
	repoTagsCache[repo] = tags
	return tags, nil
}

func getLatestTag(repo string) (Tag, error) {
	tags, err := listSemverTags(repo)
	if err != nil || len(tags) == 0 {
		return Tag{}, err
	}
	return tags[len(tags)-1], nil
}

func getTag(repo, ref string) (Tag, error) {
	tags, err := listSemverTags(repo)
	if err != nil {
		return Tag{}, fmt.Errorf("github: tag: %s@%s: %w", repo, ref, err)
	}
	resolvedRef := ref
	for _, tag := range tags {
		if tag.Name == ref {
			resolvedRef = tag.Object.SHA
			break
		}
	}
	var candidates []Tag
	for _, tag := range tags {
		if tag.Object.SHA == resolvedRef {
			candidates = append(candidates, tag)
		}
	}
	if len(candidates) == 0 {
		return Tag{}, nil
	}
	return candidates[len(candidates)-1], nil
}

func resetCaches() {
	refCache = map[string]Object{}
	repoTagsCache = map[string][]Tag{}
}

func isYaml(path string) bool {
	ext := filepath.Ext(path)
	return ext == ".yml" || ext == ".yaml"
}

func githubJSON[T any](url string) (T, int, error) {
	var zero T
	r, status, err := getGH(url)
	if err != nil {
		return zero, status, err
	}
	defer r.Close() //nolint:errcheck
	if status != http.StatusOK {
		return zero, status, nil
	}
	var v T
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		return zero, status, err
	}
	return v, status, nil
}

func getGH(url string) (io.ReadCloser, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "pinata")
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	return resp.Body, resp.StatusCode, nil
}

func isSHA(s string) bool {
	if len(s) != shaLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
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
