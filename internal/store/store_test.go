package store

import (
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir() + "/nested")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	if err := s.Load("none.json", &missing); err != nil || missing != nil {
		t.Fatalf("missing file: %v %v", missing, err)
	}
	want := map[string]int{"a": 1}
	if err := s.Save("x.json", want); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(s.Path("x.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	var got map[string]int
	if err := s.Load("x.json", &got); err != nil || got["a"] != 1 {
		t.Fatalf("got %v %v", got, err)
	}
	if err := os.WriteFile(s.Path("bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Load("bad.json", &got); err == nil {
		t.Fatal("corrupt file should fail")
	}
	entries, _ := os.ReadDir(s.dir)
	if len(entries) != 2 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
