package seed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	s, err := Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Users) != 5 || len(s.Services) != 6 || len(s.Steps) != 11 || len(s.Docs) != 7 || len(s.Items) == 0 || len(s.Roles) == 0 {
		t.Fatalf("seed counts: users %d services %d steps %d docs %d", len(s.Users), len(s.Services), len(s.Steps), len(s.Docs))
	}
	for _, d := range s.Docs {
		if !strings.Contains(d.Body, "не регламент Авито") {
			t.Errorf("%s: test instruction must be marked as not an Avito regulation", d.ID)
		}
	}
}

func TestLoad_Errors(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("missing files accepted")
	}
	dir := t.TempDir()
	for _, name := range []string{"users", "services", "roles", "equipment", "onboarding"} {
		writeFile(t, filepath.Join(dir, "seed", name+".yaml"), "{}\n")
	}
	writeFile(t, filepath.Join(dir, "seed", "roles.yaml"), "roles: []\nunknown: 1\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "roles.yaml") {
		t.Fatalf("unknown field accepted: %v", err)
	}
	writeFile(t, filepath.Join(dir, "seed", "roles.yaml"), "roles: []\n")
	writeFile(t, filepath.Join(dir, "kb", "bad.md"), "no front matter")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "bad.md") {
		t.Fatalf("bad doc accepted: %v", err)
	}
}

func TestParseDoc(t *testing.T) {
	doc, err := parseDoc("x", "---\r\ntitle: T\r\nservice: vpn\r\ntags: [a]\r\n---\r\nbody\r\n")
	if err != nil || doc.Title != "T" || doc.Body != "body" || doc.Tags[0] != "a" {
		t.Fatalf("doc = %+v, err = %v", doc, err)
	}
	for name, text := range map[string]string{
		"no front":   "body",
		"not closed": "---\ntitle: T\n",
		"bad yaml":   "---\ntitle: [\n---\nbody",
		"no title":   "---\nservice: vpn\n---\nbody",
	} {
		if _, err := parseDoc("x", text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
