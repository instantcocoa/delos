package promptfile

import (
	"fmt"
	"strings"
)

// Change is one semantic difference between two prompt files.
type Change struct {
	Field  string // e.g. "messages[0]", "config.temperature", "name"
	Kind   string // "added" | "removed" | "changed" | "whitespace"
	Detail string // human-readable description
}

func (c Change) String() string {
	return fmt.Sprintf("%s %s: %s", c.Kind, c.Field, c.Detail)
}

// SemanticDiff compares two prompt files at the file-format level: message
// edits are classified (whitespace-only vs content), and scalar fields are
// compared directly.
func SemanticDiff(a, b *PromptFile) []Change {
	var changes []Change

	scalar := func(field, av, bv string) {
		if av != bv {
			changes = append(changes, Change{
				Field: field, Kind: "changed",
				Detail: fmt.Sprintf("%q -> %q", truncate(av, 60), truncate(bv, 60)),
			})
		}
	}
	scalar("name", a.Name, b.Name)
	scalar("description", a.Description, b.Description)

	// messages: index-aligned comparison plus added/removed tails
	common := min(len(a.Messages), len(b.Messages))
	for i := 0; i < common; i++ {
		am, bm := a.Messages[i], b.Messages[i]
		field := fmt.Sprintf("messages[%d]", i)
		if am.Role != bm.Role {
			changes = append(changes, Change{Field: field, Kind: "changed",
				Detail: fmt.Sprintf("role %q -> %q", am.Role, bm.Role)})
			continue
		}
		if am.Content == bm.Content {
			continue
		}
		if squashSpace(am.Content) == squashSpace(bm.Content) {
			changes = append(changes, Change{Field: field, Kind: "whitespace",
				Detail: "whitespace-only change"})
		} else {
			changes = append(changes, Change{Field: field, Kind: "changed",
				Detail: fmt.Sprintf("content %q -> %q", truncate(am.Content, 40), truncate(bm.Content, 40))})
		}
	}
	for i := common; i < len(b.Messages); i++ {
		changes = append(changes, Change{Field: fmt.Sprintf("messages[%d]", i), Kind: "added",
			Detail: fmt.Sprintf("%s message %q", b.Messages[i].Role, truncate(b.Messages[i].Content, 40))})
	}
	for i := common; i < len(a.Messages); i++ {
		changes = append(changes, Change{Field: fmt.Sprintf("messages[%d]", i), Kind: "removed",
			Detail: fmt.Sprintf("%s message %q", a.Messages[i].Role, truncate(a.Messages[i].Content, 40))})
	}

	// variables by name
	avars := map[string]Variable{}
	for _, v := range a.Variables {
		avars[v.Name] = v
	}
	bvars := map[string]Variable{}
	for _, v := range b.Variables {
		bvars[v.Name] = v
	}
	for name := range bvars {
		if _, ok := avars[name]; !ok {
			changes = append(changes, Change{Field: "variables." + name, Kind: "added", Detail: "variable added"})
		}
	}
	for name := range avars {
		if _, ok := bvars[name]; !ok {
			changes = append(changes, Change{Field: "variables." + name, Kind: "removed", Detail: "variable removed"})
		}
	}

	// config
	ac, bc := a.Config, b.Config
	if (ac == nil) != (bc == nil) {
		changes = append(changes, Change{Field: "config", Kind: "changed", Detail: "generation config added or removed"})
	} else if ac != nil && bc != nil {
		cmpFloat := func(field string, x, y *float64) {
			if (x == nil) != (y == nil) || (x != nil && *x != *y) {
				changes = append(changes, Change{Field: "config." + field, Kind: "changed",
					Detail: fmt.Sprintf("%s -> %s", floatStr(x), floatStr(y))})
			}
		}
		cmpFloat("temperature", ac.Temperature, bc.Temperature)
		cmpFloat("top_p", ac.TopP, bc.TopP)
		if (ac.MaxTokens == nil) != (bc.MaxTokens == nil) || (ac.MaxTokens != nil && *ac.MaxTokens != *bc.MaxTokens) {
			changes = append(changes, Change{Field: "config.max_tokens", Kind: "changed", Detail: "max_tokens changed"})
		}
	}

	return changes
}

// Render substitutes {{variable}} placeholders using the declared variables:
// required variables must be provided, optional ones fall back to defaults.
func Render(pf *PromptFile, vars map[string]string) ([]Message, error) {
	values := map[string]string{}
	for _, v := range pf.Variables {
		if v.Default != nil {
			values[v.Name] = *v.Default
		}
	}
	for k, v := range vars {
		values[k] = v
	}
	for _, v := range pf.Variables {
		if _, ok := values[v.Name]; v.Required && !ok {
			return nil, fmt.Errorf("required variable %q not provided (use --var %s=...)", v.Name, v.Name)
		}
	}

	out := make([]Message, len(pf.Messages))
	for i, m := range pf.Messages {
		content := m.Content
		for k, v := range values {
			content = strings.ReplaceAll(content, "{{"+k+"}}", v)
		}
		out[i] = Message{Role: m.Role, Content: content}
	}
	return out, nil
}

func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func floatStr(f *float64) string {
	if f == nil {
		return "unset"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", *f), "0"), ".")
}
