package rag

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxBriefFiles    = 250
	maxBriefPreview  = 80
	maxBriefKeyFiles = 20
)

func BuildProjectBrief(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		workDir = "."
	}

	var files []string
	langCounts := make(map[string]int)
	var keyFiles []string

	err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "vendor", "dist", "build", ".idea", ".vscode":
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || strings.HasPrefix(rel, "..") {
			return nil
		}

		if len(files) < maxBriefFiles {
			files = append(files, rel)
		}

		lang := languageForFile(rel)
		if lang != "" {
			langCounts[lang]++
		}
		if isKeyProjectFile(rel) && len(keyFiles) < maxBriefKeyFiles {
			keyFiles = append(keyFiles, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Strings(files)
	sort.Strings(keyFiles)

	var languageSummary []string
	for lang, count := range langCounts {
		languageSummary = append(languageSummary, fmt.Sprintf("%s(%d)", lang, count))
	}
	sort.Strings(languageSummary)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Working directory: %s\n", filepath.Clean(workDir)))
	if len(languageSummary) > 0 {
		b.WriteString("Detected languages: " + strings.Join(languageSummary, ", ") + "\n")
	} else {
		b.WriteString("Detected languages: unknown\n")
	}

	if len(keyFiles) > 0 {
		b.WriteString("Key files:\n")
		for _, file := range keyFiles {
			b.WriteString("- " + file + "\n")
		}
	}

	b.WriteString("File tree preview:\n")
	preview := files
	if len(preview) > maxBriefPreview {
		preview = preview[:maxBriefPreview]
	}
	for _, file := range preview {
		b.WriteString("- " + file + "\n")
	}
	if len(files) > len(preview) {
		b.WriteString(fmt.Sprintf("- ... (%d more files)\n", len(files)-len(preview)))
	}

	return strings.TrimSpace(b.String()), nil
}

func languageForFile(relPath string) string {
	name := strings.ToLower(filepath.Base(relPath))
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go":
		return "Go"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".md":
		return "Markdown"
	case ".json", ".yaml", ".yml", ".toml":
		return "Config"
	case ".sh":
		return "Shell"
	default:
		if name == "makefile" {
			return "Build"
		}
	}
	return ""
}

func isKeyProjectFile(relPath string) bool {
	lower := strings.ToLower(relPath)
	if strings.HasSuffix(lower, "/go.mod") || strings.HasSuffix(lower, "/go.sum") {
		return true
	}
	if strings.HasSuffix(lower, "/readme.md") || strings.HasSuffix(lower, "/makefile") {
		return true
	}
	if strings.HasPrefix(lower, "cmd/") || strings.HasPrefix(lower, "internal/") {
		return true
	}
	return false
}
