package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type DiffLine struct {
	Type    string `json:"type"` // "add", "remove", "context"
	OldLine *int   `json:"oldLine"`
	NewLine *int   `json:"newLine"`
	Content string `json:"content"`
}

type DiffHunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

type DiffFile struct {
	Path      string     `json:"path"`
	Status    string     `json:"status"` // "added", "modified", "deleted"
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Hunks     []DiffHunk `json:"hunks"`
}

type DiffSnapshot struct {
	RepoFullName string     `json:"repoFullName"`
	Branch       string     `json:"branch"`
	Files        []DiffFile `json:"files"`
}

type RepoInfo struct {
	RepoFullName string `json:"repoFullName"`
	Branch       string `json:"branch"`
	ChangedCount int    `json:"changedFilesCount"`
}

type Service struct {
	RepoDir string
}

func NewService(repoDir string) *Service {
	return &Service{RepoDir: repoDir}
}

func (s *Service) runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.RepoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

func (s *Service) Info() RepoInfo {
	branch, err := s.runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || strings.TrimSpace(branch) == "" {
		branch = "main"
	} else {
		branch = strings.TrimSpace(branch)
	}

	repoName := filepath.Base(s.RepoDir)
	if remote, err := s.runGit("config", "--get", "remote.origin.url"); err == nil {
		if parsed := parseRepoFullName(strings.TrimSpace(remote)); parsed != "" {
			repoName = parsed
		}
	}

	statusOut, _ := s.runGit("status", "--porcelain")
	changed := 0
	for _, line := range strings.Split(statusOut, "\n") {
		if strings.TrimSpace(line) != "" {
			changed++
		}
	}

	return RepoInfo{
		RepoFullName: repoName,
		Branch:       branch,
		ChangedCount: changed,
	}
}

func (s *Service) Diff() (DiffSnapshot, error) {
	info := s.Info()
	rawDiff, err := s.runGit("diff", "--no-color", "HEAD")
	if err != nil {
		// Repository might have no commits yet; fall back to unstaged diff.
		rawDiff, _ = s.runGit("diff", "--no-color")
	}

	files := ParseUnifiedDiff(rawDiff)
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f.Path] = true
	}

	// Include untracked files as added diffs
	untrackedOut, err := s.runGit("ls-files", "--others", "--exclude-standard")
	if err == nil {
		for _, rel := range strings.Split(untrackedOut, "\n") {
			rel = strings.TrimSpace(rel)
			if rel == "" || seen[rel] {
				continue
			}
			if df, ok := s.buildUntrackedDiff(rel); ok {
				files = append(files, df)
			}
		}
	}

	return DiffSnapshot{
		RepoFullName: info.RepoFullName,
		Branch:       info.Branch,
		Files:        files,
	}, nil
}

func (s *Service) buildUntrackedDiff(relPath string) (DiffFile, bool) {
	full := filepath.Join(s.RepoDir, filepath.FromSlash(relPath))
	data, err := os.ReadFile(full)
	if err != nil || len(data) > 512*1024 || bytes.IndexByte(data, 0) != -1 {
		return DiffFile{}, false
	}
	rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lines := make([]DiffLine, 0, len(rawLines))
	for i, text := range rawLines {
		ln := i + 1
		lines = append(lines, DiffLine{
			Type:    "add",
			NewLine: &ln,
			Content: text,
		})
	}
	return DiffFile{
		Path:      filepath.ToSlash(relPath),
		Status:    "added",
		Additions: len(lines),
		Deletions: 0,
		Hunks: []DiffHunk{
			{
				Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)),
				Lines:  lines,
			},
		},
	}, true
}

func (s *Service) CommitAndPush(message string, push bool) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("commit message wajib diisi")
	}
	if _, err := s.runGit("add", "-A"); err != nil {
		return "", fmt.Errorf("git add gagal: %w", err)
	}
	commitOut, err := s.runGit("commit", "-m", msg)
	if err != nil {
		return "", fmt.Errorf("git commit gagal: %w", err)
	}
	if !push {
		return strings.TrimSpace(commitOut), nil
	}
	pushOut, err := s.runGit("push")
	if err != nil {
		return "", fmt.Errorf("commit sukses tapi git push gagal: %w", err)
	}
	return strings.TrimSpace(commitOut + "\n" + pushOut), nil
}

func (s *Service) Reset(relPath string) error {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		if _, err := s.runGit("checkout", "--", "."); err != nil {
			return err
		}
		_, err := s.runGit("clean", "-fd")
		return err
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return errors.New("path file tidak valid")
	}
	if _, err := s.runGit("checkout", "--", clean); err != nil {
		// Might be an untracked file; remove safely within repo dir.
		full := filepath.Join(s.RepoDir, clean)
		return os.Remove(full)
	}
	return nil
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// ParseUnifiedDiff parses `git diff` output with a 4MB line buffer and binary-file guard.
func ParseUnifiedDiff(raw string) []DiffFile {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var files []DiffFile
	var current *DiffFile
	var currentHunk *DiffHunk
	oldLine, newLine := 0, 0

	flushHunk := func() {
		if current != nil && currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if current != nil {
			files = append(files, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			parts := strings.Split(line, " ")
			path := ""
			if len(parts) >= 4 {
				path = strings.TrimPrefix(parts[3], "b/")
			}
			current = &DiffFile{
				Path:   path,
				Status: "modified",
				Hunks:  []DiffHunk{},
			}
		case current == nil:
			continue
		case strings.HasPrefix(line, "new file mode"):
			current.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			current.Status = "deleted"
		case strings.HasPrefix(line, "Binary files "):
			// Guard against binary files: keep entry with empty hunks.
			flushHunk()
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			if m := hunkHeaderRe.FindStringSubmatch(line); len(m) == 3 {
				oldLine, _ = strconv.Atoi(m[1])
				newLine, _ = strconv.Atoi(m[2])
			}
			currentHunk = &DiffHunk{
				Header: line,
				Lines:  []DiffLine{},
			}
		case currentHunk != nil:
			if strings.HasPrefix(line, "+") {
				ln := newLine
				newLine++
				current.Additions++
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "add",
					NewLine: &ln,
					Content: line[1:],
				})
			} else if strings.HasPrefix(line, "-") {
				ln := oldLine
				oldLine++
				current.Deletions++
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "remove",
					OldLine: &ln,
					Content: line[1:],
				})
			} else if strings.HasPrefix(line, " ") {
				ol, nl := oldLine, newLine
				oldLine++
				newLine++
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    "context",
					OldLine: &ol,
					NewLine: &nl,
					Content: line[1:],
				})
			}
		}
	}
	flushFile()
	return files
}

func parseRepoFullName(remoteURL string) string {
	cleaned := strings.TrimSuffix(remoteURL, ".git")
	if idx := strings.Index(cleaned, "github.com:"); idx != -1 {
		return strings.TrimPrefix(cleaned[idx+len("github.com:"):], "/")
	}
	if idx := strings.Index(cleaned, "github.com/"); idx != -1 {
		return strings.TrimPrefix(cleaned[idx+len("github.com/"):], "/")
	}
	return ""
}
