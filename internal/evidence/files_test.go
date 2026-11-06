package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abevz/dibs/internal/core"
)

func TestCopyAndCleanupSources(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", filepath.Join(t.TempDir(), "evidence"))
	dir := t.TempDir()
	source := filepath.Join(dir, "log.txt")
	unknown := filepath.Join(dir, "unknown.txt")
	for _, p := range []string{source, unknown} {
		if err := os.WriteFile(p, []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Copy(source, "project", "issue", "id")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CleanupSource(e, false); err != nil || !ok {
		t.Fatalf("preview: %v %v", ok, err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := CleanupSource(e, true); err != nil || ok {
		t.Fatalf("changed source removed: %v %v", ok, err)
	}
	if err = os.WriteFile(source, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := CleanupSource(e, true); err != nil || !ok {
		t.Fatalf("cleanup: %v %v", ok, err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("temporary source remains")
	}
	if _, err = os.Stat(unknown); err != nil {
		t.Fatal("unknown file removed")
	}
	if _, err = os.Stat(e.Path); err != nil {
		t.Fatal("durable copy removed")
	}
	if err = Remove(unknown); err == nil {
		t.Fatal("removed unmanaged file")
	}
	link := filepath.Join(dir, "symlink")
	if err = os.Symlink(e.Path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Copy(link, "project", "issue", "symlink"); err == nil {
		t.Fatal("accepted symlink source")
	}
	if _, err = Copy(dir, "project", "issue", "directory"); err == nil {
		t.Fatal("accepted directory source")
	}
}
func TestCheckRequiresIntactPassReview(t *testing.T) {
	t.Setenv("DIBS_EVIDENCE_DIR", t.TempDir())
	src := filepath.Join(t.TempDir(), "review")
	if err := os.WriteFile(src, []byte("review"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Copy(src, "p", "i", "id")
	if err != nil {
		t.Fatal(err)
	}
	e.Kind = "review"
	e.DiffHash = strings.Repeat("a", 64)
	for _, status := range []string{"", "FAIL", "PASS"} {
		e.Status = status
		err = Check([]core.Evidence{e}, []string{"review"}, e.DiffHash)
		if (err == nil) != (status == "PASS") {
			t.Fatalf("status=%s err=%v", status, err)
		}
	}
	e.PrunedAt = "2026-01-01T00:00:00Z"
	if Check([]core.Evidence{e}, []string{"review"}, e.DiffHash) == nil {
		t.Fatal("pruned evidence accepted")
	}
}
