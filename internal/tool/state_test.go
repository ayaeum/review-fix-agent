package tool

import (
	"sync"
	"testing"
)

func TestReadStateRecordGet(t *testing.T) {
	rs := NewReadState()
	if _, ok := rs.Get("a.go"); ok {
		t.Error("Get on empty state should report not found")
	}
	rs.Record("a.go", ReadRecord{Content: "hello", ModUnix: 42})
	rec, ok := rs.Get("a.go")
	if !ok || rec.Content != "hello" || rec.ModUnix != 42 {
		t.Errorf("Get = %+v, %v", rec, ok)
	}
	// Re-record overwrites.
	rs.Record("a.go", ReadRecord{Content: "world", ModUnix: 7})
	if rec, _ := rs.Get("a.go"); rec.Content != "world" || rec.ModUnix != 7 {
		t.Errorf("overwrite failed: %+v", rec)
	}
}

func TestSinkFindingsAndFix(t *testing.T) {
	s := NewSink()
	if s.HasFindings() || s.HasFix() {
		t.Error("empty sink should have neither findings nor fix")
	}
	s.SetFindings(map[string]any{"findings": []any{}})
	if !s.HasFindings() {
		t.Error("HasFindings should be true after SetFindings")
	}
	s.SetFix(map[string]any{"summary": "x"})
	if !s.HasFix() {
		t.Error("HasFix should be true after SetFix")
	}
}

func TestSinkChangedFilesSortedAndDeduped(t *testing.T) {
	s := NewSink()
	s.RecordChangedFile("c.go")
	s.RecordChangedFile("a.go")
	s.RecordChangedFile("a.go") // duplicate
	s.RecordChangedFile("b.go")
	got := s.ChangedFiles()
	want := []string{"a.go", "b.go", "c.go"}
	if len(got) != len(want) {
		t.Fatalf("ChangedFiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ChangedFiles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSinkCommandRecordsCopy(t *testing.T) {
	s := NewSink()
	s.RecordCommand(CommandRecord{Command: "go test", Passed: true, Summary: "ok"})
	recs := s.CommandRecords()
	if len(recs) != 1 || recs[0].Command != "go test" || !recs[0].Passed {
		t.Fatalf("CommandRecords = %+v", recs)
	}
	// Mutating the returned slice must not affect the sink's internal state.
	recs[0].Command = "MUTATED"
	if again := s.CommandRecords(); again[0].Command != "go test" {
		t.Errorf("CommandRecords returned an aliased slice; got %q", again[0].Command)
	}
}

// TestSinkAndReadStateConcurrent exercises the mutexes; run with -race.
func TestSinkAndReadStateConcurrent(t *testing.T) {
	s := NewSink()
	rs := NewReadState()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.RecordChangedFile("f.go")
			s.RecordCommand(CommandRecord{Command: "c", Passed: true})
			_ = s.HasFindings()
			_ = s.ChangedFiles()
			rs.Record("f.go", ReadRecord{Content: "x"})
			_, _ = rs.Get("f.go")
		}(i)
	}
	wg.Wait()
	if files := s.ChangedFiles(); len(files) != 1 || files[0] != "f.go" {
		t.Errorf("ChangedFiles after concurrent = %v", files)
	}
	if recs := s.CommandRecords(); len(recs) != 50 {
		t.Errorf("expected 50 command records, got %d", len(recs))
	}
}
