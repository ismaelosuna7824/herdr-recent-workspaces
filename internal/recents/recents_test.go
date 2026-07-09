package recents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTouchUpsertsAndOrders(t *testing.T) {
	s := &Store{}
	s.Touch("/a", "A", 100)
	s.Touch("/b", "B", 200)
	s.Touch("/a", "A", 300) // re-open a, now newest

	got := s.Sorted()
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Path != "/a" || got[0].LastOpened != 300 {
		t.Fatalf("expected /a newest at 300, got %+v", got[0])
	}
	if got[1].Path != "/b" {
		t.Fatalf("expected /b second, got %+v", got[1])
	}
}

func TestSeedDoesNotBumpExisting(t *testing.T) {
	s := &Store{}
	s.Touch("/a", "A", 100)
	s.Seed("/a", "A", 999) // must not move /a to 999
	s.Seed("/b", "B", 50)  // new, added at 50

	got := s.Sorted()
	if got[0].Path != "/a" || got[0].LastOpened != 100 {
		t.Fatalf("seed bumped existing entry: %+v", got[0])
	}
	if len(got) != 2 || got[1].Path != "/b" {
		t.Fatalf("seed did not add new entry: %+v", got)
	}
}

func TestSeedFillsMissingLabel(t *testing.T) {
	s := &Store{}
	s.Touch("/a", "", 100)
	s.Seed("/a", "Named", 200)
	if s.Sorted()[0].Label != "Named" {
		t.Fatalf("seed should backfill an empty label")
	}
}

func TestRemove(t *testing.T) {
	s := &Store{}
	s.Touch("/a", "A", 100)
	s.Touch("/b", "B", 200)
	if !s.Remove("/a") {
		t.Fatal("expected Remove to report true")
	}
	if s.Remove("/a") {
		t.Fatal("removing twice should report false")
	}
	if len(s.Sorted()) != 1 || s.Sorted()[0].Path != "/b" {
		t.Fatalf("remove left wrong state: %+v", s.Sorted())
	}
}

func TestPruneMissing(t *testing.T) {
	dir := t.TempDir()
	s := &Store{}
	s.Touch(dir, "real", 100)
	s.Touch(filepath.Join(dir, "does-not-exist"), "gone", 200)
	s.PruneMissing()
	got := s.Sorted()
	if len(got) != 1 || got[0].Path != dir {
		t.Fatalf("prune kept wrong entries: %+v", got)
	}
}

func TestNormalizeOnTouch(t *testing.T) {
	s := &Store{}
	s.Touch("/a/b/../b", "A", 100) // cleans to /a/b
	s.Touch("/a/b", "A", 200)      // same path, must upsert not duplicate
	if len(s.Sorted()) != 1 {
		t.Fatalf("path normalization failed, got %+v", s.Sorted())
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "recents.json")
	s := Load(path)
	s.Touch("/a", "A", 100)
	s.Touch("/b", "B", 200)
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	reloaded := Load(path)
	got := reloaded.Sorted()
	if len(got) != 2 || got[0].Path != "/b" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}
