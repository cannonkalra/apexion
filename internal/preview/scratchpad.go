package preview

import (
	"context"
	"fmt"
	"strings"
)

// allowedLeadingKeywords are the only statement kinds the scratchpad permits.
var allowedLeadingKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "DESCRIBE": true, "SUMMARIZE": true,
	"EXPLAIN": true, "SHOW": true, "PRAGMA": true, "TABLE": true, "VALUES": true,
}

// forbiddenTokens are never allowed anywhere in a scratchpad query.
var forbiddenTokens = []string{
	"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "ATTACH", "DETACH",
	"COPY", "INSTALL", "LOAD", "SET ", "CALL", "EXPORT", "IMPORT", "TRUNCATE",
	"REPLACE", "GRANT", "REVOKE", "VACUUM", "CHECKPOINT",
}

// ValidateSQL enforces a read-only subset of DuckDB SQL for the scratchpad.
func ValidateSQL(query string) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return fmt.Errorf("empty query")
	}
	// Disallow multiple statements (allow a single trailing semicolon).
	if strings.Count(strings.TrimRight(q, "; \n\t"), ";") > 0 {
		return fmt.Errorf("only a single statement is allowed")
	}
	q = strings.TrimRight(q, "; \n\t")

	upper := strings.ToUpper(q)
	first := firstWord(upper)
	if !allowedLeadingKeywords[first] {
		return fmt.Errorf("only read-only queries are allowed (SELECT, WITH, DESCRIBE, SUMMARIZE, SHOW, EXPLAIN); got %q", first)
	}
	for _, tok := range forbiddenTokens {
		if strings.Contains(upper, tok) {
			return fmt.Errorf("statement contains a disallowed keyword: %s", strings.TrimSpace(tok))
		}
	}
	return nil
}

// RunSQL validates and executes a read-only scratchpad query against the S3-
// backed DuckDB engine, applying a row limit.
func (e *Engine) RunSQL(ctx context.Context, query string, limit int) (*Result, error) {
	if !e.ready {
		return nil, fmt.Errorf("SQL engine unavailable: %s", e.readErr)
	}
	if err := ValidateSQL(query); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return e.query(ctx, strings.TrimRight(strings.TrimSpace(query), "; \n\t"), limit)
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n("); i > 0 {
		return s[:i]
	}
	return s
}
