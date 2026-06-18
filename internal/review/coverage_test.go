package review

import "testing"

func TestFilteredDedups(t *testing.T) {
	r := Report{
		Findings: []Finding{
			{File: "a.go", Line: 1, Title: "X", Severity: "high", Evidence: "e", Impact: "i"},
			{File: "a.go", Line: 1, Title: "X", Severity: "high", Evidence: "e", Impact: "i"}, // exact dup
			{File: "a.go", Line: 2, Title: "Y", Severity: "low", Evidence: "e", Impact: "i"},
		},
		ReviewedScope: []string{"a.go"},
		Verification:  "ran",
	}
	f := r.Filtered()
	if len(f.Findings) != 2 {
		t.Fatalf("Filtered did not dedup: %d findings", len(f.Findings))
	}
	if len(f.ReviewedScope) != 1 || f.Verification != "ran" {
		t.Errorf("Filtered dropped scope/verification: %+v", f)
	}
}

func TestToPayloadRoundTrip(t *testing.T) {
	r := Report{
		Findings:      []Finding{{Severity: "high", File: "a.go", Line: 3, Title: "t", Evidence: "e", Impact: "i", Confidence: "high"}},
		ReviewedScope: []string{"a.go"},
		Verification:  "not run",
	}
	m, err := ToPayload(r)
	if err != nil {
		t.Fatalf("ToPayload: %v", err)
	}
	back, err := ParseReport(m)
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if len(back.Findings) != 1 || back.Findings[0] != r.Findings[0] {
		t.Errorf("round trip mismatch: %+v", back.Findings)
	}
	if back.Verification != "not run" {
		t.Errorf("verification lost in round trip: %q", back.Verification)
	}
}

func TestParseFilterIDs(t *testing.T) {
	ids := parseFilterIDs(`["f-0","f-2","f-99","garbage"]`, 3)
	if len(ids) != 2 {
		t.Fatalf("parseFilterIDs = %v, want {0,2}", ids)
	}
	if _, ok := ids[0]; !ok {
		t.Error("f-0 should be parsed")
	}
	if _, ok := ids[2]; !ok {
		t.Error("f-2 should be parsed")
	}
	if _, ok := ids[99]; ok {
		t.Error("f-99 is out of range (total 3) and must be ignored")
	}
	// Fenced JSON is tolerated.
	if ids := parseFilterIDs("```json\n[\"f-1\"]\n```", 3); len(ids) != 1 {
		t.Errorf("fenced parse failed: %v", ids)
	}
	// Non-JSON yields nil.
	if ids := parseFilterIDs("not json", 3); ids != nil {
		t.Errorf("invalid input should yield nil, got %v", ids)
	}
}

func TestStripJSONFences(t *testing.T) {
	cases := map[string]string{
		"```json\n[1,2]\n```": "[1,2]",
		"```\n[1]\n```":       "[1]",
		"[3]":                 "[3]",
		"  [4]  ":             "[4]",
	}
	for in, want := range cases {
		if got := stripJSONFences(in); got != want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", in, got, want)
		}
	}
}
