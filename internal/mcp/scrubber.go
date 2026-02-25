package mcp

import "regexp"

var (
	// Matches typical .env VAR=value lines
	// The $1 backreference keeps the VAR= part and replaces value with [REDACTED]
	envRegex = regexp.MustCompile(`(?m)^([A-Z_]+)=\S+$`)
	// JWT token heuristic
	jwtRegex = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`)
	// OpenAI and generic sk- keys
	skRegex = regexp.MustCompile(`sk-[a-zA-Z0-9\-]{20,}`)
	// Google API keys
	aizaRegex = regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`)
	// GitHub personal access tokens
	ghpRegex = regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`)
)

// Clean scrubs secrets from the outbound text before sending it to an LLM provider.
func Clean(input string) string {
	input = envRegex.ReplaceAllString(input, "${1}=[REDACTED]")
	input = skRegex.ReplaceAllString(input, "[REDACTED_KEY]")
	input = jwtRegex.ReplaceAllString(input, "[REDACTED_JWT]")
	input = aizaRegex.ReplaceAllString(input, "[REDACTED_KEY]")
	input = ghpRegex.ReplaceAllString(input, "[REDACTED_KEY]")
	return input
}
