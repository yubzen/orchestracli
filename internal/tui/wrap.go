package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func wrapToWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	wrapper := lipgloss.NewStyle().Width(width)

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		wrapped := wrapper.Render(line)
		for _, wrappedLine := range strings.Split(wrapped, "\n") {
			out = append(out, strings.TrimRight(wrappedLine, " "))
		}
	}
	return strings.Join(out, "\n")
}

func wrapWithPrefix(prefix, content string, width int) string {
	if width <= 0 {
		return prefix + content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")

	prefixWidth := lipgloss.Width(prefix)
	if prefixWidth >= width {
		return wrapToWidth(prefix+content, width)
	}

	contentWidth := width - prefixWidth
	wrappedContent := wrapToWidth(content, contentWidth)
	contentLines := strings.Split(wrappedContent, "\n")
	if len(contentLines) == 0 {
		return prefix
	}

	indent := strings.Repeat(" ", prefixWidth)
	for i := range contentLines {
		if i == 0 {
			contentLines[i] = prefix + contentLines[i]
			continue
		}
		contentLines[i] = indent + contentLines[i]
	}
	return strings.Join(contentLines, "\n")
}
