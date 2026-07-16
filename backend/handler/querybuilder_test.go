package handler

import (
	"testing"
)

func TestBuildLogWhereClauses_NoFilters(t *testing.T) {
	opts := LogFilterOptions{}
	clauses, args, idx := buildLogWhereClauses(opts)

	if len(clauses) != 0 {
		t.Errorf("expected 0 clauses, got %d", len(clauses))
	}
	if len(args) != 0 {
		t.Errorf("expected 0 args, got %d", len(args))
	}
	if idx != 1 {
		t.Errorf("expected idx 1, got %d", idx)
	}
}

func TestBuildLogWhereClauses_Hostname(t *testing.T) {
	opts := LogFilterOptions{Hostname: "server1"}
	clauses, args, idx := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	if clauses[0] != "hostname = $1" {
		t.Errorf("expected %q, got %q", "hostname = $1", clauses[0])
	}
	if len(args) != 1 || args[0] != "server1" {
		t.Errorf("expected arg [server1], got %v", args)
	}
	if idx != 2 {
		t.Errorf("expected idx 2, got %d", idx)
	}
}

func TestBuildLogWhereClauses_FromHostIP(t *testing.T) {
	t.Run("normal IP", func(t *testing.T) {
		opts := LogFilterOptions{FromHostIP: "192.168.1.1"}
		clauses, _, idx := buildLogWhereClauses(opts)

		if len(clauses) != 1 {
			t.Errorf("expected 1 clause, got %d", len(clauses))
		}
		if clauses[0] != "COALESCE(fromhost_ip, '') = $1" {
			t.Errorf("expected COALESCE clause, got %q", clauses[0])
		}
		if idx != 2 {
			t.Errorf("expected idx 2, got %d", idx)
		}
	})

	t.Run("unknown marker", func(t *testing.T) {
		opts := LogFilterOptions{FromHostIP: "__unknown__"}
		clauses, args, idx := buildLogWhereClauses(opts)

		if len(clauses) != 1 {
			t.Errorf("expected 1 clause, got %d", len(clauses))
		}
		if len(args) != 0 {
			t.Errorf("expected 0 args for __unknown__, got %d", len(args))
		}
		if idx != 1 {
			t.Errorf("expected idx 1, got %d", idx)
		}
	})
}

func TestBuildLogWhereClauses_Severity(t *testing.T) {
	opts := LogFilterOptions{Severity: "ERR"}
	clauses, args, idx := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	if clauses[0] != "severity = $1" {
		t.Errorf("expected %q, got %q", "severity = $1", clauses[0])
	}
	if args[0] != "ERR" {
		t.Errorf("expected arg ERR, got %v", args[0])
	}
	if idx != 2 {
		t.Errorf("expected idx 2, got %d", idx)
	}
}

func TestBuildLogWhereClauses_AppName(t *testing.T) {
	opts := LogFilterOptions{AppName: "sshd"}
	clauses, args, _ := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	if clauses[0] != "app_name ILIKE $1" {
		t.Errorf("expected ILIKE clause, got %q", clauses[0])
	}
	if args[0] != "%sshd%" {
		t.Errorf("expected %%sshd%%, got %v", args[0])
	}
}

func TestBuildLogWhereClauses_DateRange(t *testing.T) {
	opts := LogFilterOptions{
		From: "2024-01-01",
		To:   "2024-12-31",
	}
	clauses, _, idx := buildLogWhereClauses(opts)

	if len(clauses) != 2 {
		t.Errorf("expected 2 clauses, got %d", len(clauses))
	}
	if clauses[0] != "timestamp >= $1" {
		t.Errorf("expected >= clause, got %q", clauses[0])
	}
	if clauses[1] != "timestamp <= $2" {
		t.Errorf("expected <= clause, got %q", clauses[1])
	}
	if idx != 3 {
		t.Errorf("expected idx 3, got %d", idx)
	}
}

func TestBuildLogWhereClauses_Search(t *testing.T) {
	opts := LogFilterOptions{Search: "login failed"}
	clauses, args, _ := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	if clauses[0] != "search_vector @@ websearch_to_tsquery('english', $1)" {
		t.Errorf("expected tsquery clause, got %q", clauses[0])
	}
	if args[0] != "login failed" {
		t.Errorf("expected search term, got %v", args[0])
	}
}

func TestBuildLogWhereClauses_Devices(t *testing.T) {
	opts := LogFilterOptions{Devices: []string{"10.0.0.1", "10.0.0.2"}}
	clauses, args, idx := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	expected := "COALESCE(fromhost_ip, '') IN ($1, $2)"
	if clauses[0] != expected {
		t.Errorf("expected %q, got %q", expected, clauses[0])
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %d", len(args))
	}
	if idx != 3 {
		t.Errorf("expected idx 3, got %d", idx)
	}
}

func TestBuildLogWhereClauses_HasFields(t *testing.T) {
	opts := LogFilterOptions{HasFields: true}
	clauses, _, _ := buildLogWhereClauses(opts)

	if len(clauses) != 1 {
		t.Errorf("expected 1 clause, got %d", len(clauses))
	}
	expected := "matched_parsers IS NOT NULL AND array_length(matched_parsers, 1) > 0"
	if clauses[0] != expected {
		t.Errorf("expected %q, got %q", expected, clauses[0])
	}
}

func TestBuildLogWhereClauses_Combined(t *testing.T) {
	opts := LogFilterOptions{
		Hostname: "srv1",
		Severity: "ERR",
		Search:   "fail",
	}
	clauses, args, idx := buildLogWhereClauses(opts)

	if len(clauses) != 3 {
		t.Errorf("expected 3 clauses, got %d", len(clauses))
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %d", len(args))
	}
	if idx != 4 {
		t.Errorf("expected idx 4, got %d", idx)
	}
}

func TestBuildWhereSQL(t *testing.T) {
	t.Run("empty clauses", func(t *testing.T) {
		result := buildWhereSQL([]string{})
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("single clause", func(t *testing.T) {
		result := buildWhereSQL([]string{"a = 1"})
		expected := "WHERE a = 1"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("multiple clauses", func(t *testing.T) {
		result := buildWhereSQL([]string{"a = 1", "b = 2"})
		expected := "WHERE a = 1 AND b = 2"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})
}

func TestParsePagination(t *testing.T) {
	// Note: This test requires gin.Context which needs a full setup
	// Skipping for now - covered by integration tests
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		a, b, expected string
	}{
		{"first", "second", "first"},
		{"", "second", "second"},
		{"", "", ""},
		{"a", "", "a"},
	}

	for _, tt := range tests {
		result := firstNonEmpty(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("firstNonEmpty(%q, %q) = %q, want %q", tt.a, tt.b, result, tt.expected)
		}
	}
}