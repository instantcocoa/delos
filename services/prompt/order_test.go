package prompt

import "testing"

// The ORDER BY clause is interpolated into the query string, so ListQuery.OrderBy
// must never reach it unfiltered. This test pins the allowlist: anything not
// explicitly permitted has to fall back to the default column.
func TestOrderByAllowlist(t *testing.T) {
	cases := []struct {
		orderBy string
		want    string
	}{
		{"", "p.created_at"},
		{"created_at", "p.created_at"},
		{"name", "p.name"},
		{"slug", "p.slug"},
		{"updated_at", "p.updated_at"},
		// Injection attempts and unknown columns fall back to the default.
		{"created_at; DROP TABLE prompts--", "p.created_at"},
		{"created_at, (SELECT hash FROM virtual_keys LIMIT 1)", "p.created_at"},
		{"id", "p.created_at"},
		{"'", "p.created_at"},
	}
	for _, tc := range cases {
		if got := resolveOrderBy(tc.orderBy); got != tc.want {
			t.Errorf("resolveOrderBy(%q) = %q, want %q", tc.orderBy, got, tc.want)
		}
	}
}
