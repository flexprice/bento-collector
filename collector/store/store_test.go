package store

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
)

func TestStoreVersions(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFileStore(filepath.Join(dir, "data"), filepath.Join(dir, "streams"), false)
	if err != nil {
		t.Fatal(err)
	}
	c := fixture.CPUCollector()
	for i := 0; i < 3; i++ {
		c.Name = "n" + strconv.Itoa(i)
		if _, err := st.SaveCollector(c); err != nil {
			t.Fatal(err)
		}
	}
	latest, _ := st.GetCollector(c.ID, 0)
	first, _ := st.GetCollector(c.ID, 1)
	if latest.Version != 3 || first.Name != "n0" {
		t.Fatalf("latest=%d first=%s", latest.Version, first.Name)
	}
	if _, err := st.GetCollector("../etc", 0); err == nil {
		t.Fatal("path traversal must be refused")
	}
}
