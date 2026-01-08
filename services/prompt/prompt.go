// Package prompt provides the prompt versioning service.
package prompt

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// PromptStatus represents the status of a prompt.
type PromptStatus int

const (
	PromptStatusUnspecified PromptStatus = iota
	PromptStatusDraft
	PromptStatusActive
	PromptStatusDeprecated
	PromptStatusArchived
)

// Prompt represents a versioned prompt template.
type Prompt struct {
	ID            string
	Name          string
	Slug          string // URL-friendly name
	Version       int
	Description   string
	Messages      []PromptMessage
	Variables     []PromptVariable
	DefaultConfig GenerationConfig
	Tags          []string
	Metadata      map[string]string
	Status        PromptStatus
	CreatedBy     string
	CreatedAt     time.Time
	UpdatedBy     string
	UpdatedAt     time.Time
}

// PromptMessage represents a message in a prompt template.
type PromptMessage struct {
	Role    string // system, user, assistant
	Content string // can contain {{variable}} placeholders
}

// PromptVariable represents a variable that can be interpolated.
type PromptVariable struct {
	Name         string
	Description  string
	Type         string // string, number, boolean, json
	Required     bool
	DefaultValue string
}

// GenerationConfig contains default generation parameters.
type GenerationConfig struct {
	Temperature  float64
	MaxTokens    int
	TopP         float64
	Stop         []string
	OutputSchema string // JSON schema for structured output
}

// PromptVersion represents a version in the prompt history.
type PromptVersion struct {
	Version           int
	ChangeDescription string
	UpdatedBy         string
	UpdatedAt         time.Time
}

// VersionDiff represents a difference between versions.
type VersionDiff struct {
	Field    string
	OldValue string
	NewValue string
	DiffType string // added, removed, modified
}

// ListQuery contains filters for listing prompts.
type ListQuery struct {
	Search     string
	Tags       []string
	Status     PromptStatus
	Limit      int
	Offset     int
	OrderBy    string
	Descending bool
}

// Helper functions

// GenerateID creates a new prompt ID.
func GenerateID() string {
	return fmt.Sprintf("pmt_%d", time.Now().UnixNano())
}

// Slugify converts a name to a URL-friendly slug.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// IsValidSlug checks if a slug is valid.
func IsValidSlug(s string) bool {
	return regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`).MatchString(s) ||
		regexp.MustCompile(`^[a-z0-9]$`).MatchString(s)
}

// ParseReference parses a slug:version reference.
// Returns the slug and version (0 means latest).
func ParseReference(ref string) (slug string, version int) {
	parts := strings.Split(ref, ":")
	slug = parts[0]

	if len(parts) > 1 {
		versionStr := parts[1]
		if versionStr == "latest" {
			version = 0
		} else if strings.HasPrefix(versionStr, "v") {
			fmt.Sscanf(versionStr[1:], "%d", &version)
		} else {
			fmt.Sscanf(versionStr, "%d", &version)
		}
	}

	return slug, version
}

// StatusToString converts a PromptStatus to its string representation.
func StatusToString(status PromptStatus) string {
	switch status {
	case PromptStatusDraft:
		return "draft"
	case PromptStatusActive:
		return "active"
	case PromptStatusDeprecated:
		return "deprecated"
	case PromptStatusArchived:
		return "archived"
	default:
		return "draft"
	}
}

// StringToStatus converts a string to a PromptStatus.
func StringToStatus(s string) PromptStatus {
	switch strings.ToLower(s) {
	case "draft":
		return PromptStatusDraft
	case "active":
		return PromptStatusActive
	case "deprecated":
		return PromptStatusDeprecated
	case "archived":
		return PromptStatusArchived
	default:
		return PromptStatusUnspecified
	}
}

// Truncate truncates a string to maxLen characters.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// CopyPrompt creates a deep copy of a prompt.
func CopyPrompt(p *Prompt) *Prompt {
	if p == nil {
		return nil
	}

	cp := *p
	cp.Messages = make([]PromptMessage, len(p.Messages))
	cp.Variables = make([]PromptVariable, len(p.Variables))
	cp.Tags = make([]string, len(p.Tags))
	cp.DefaultConfig.Stop = make([]string, len(p.DefaultConfig.Stop))

	copy(cp.Messages, p.Messages)
	copy(cp.Variables, p.Variables)
	copy(cp.Tags, p.Tags)
	copy(cp.DefaultConfig.Stop, p.DefaultConfig.Stop)

	if p.Metadata != nil {
		cp.Metadata = make(map[string]string)
		for k, v := range p.Metadata {
			cp.Metadata[k] = v
		}
	}

	return &cp
}
