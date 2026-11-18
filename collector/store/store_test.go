package store

import (
	"strconv"
	"testing"

	"github.com/flexprice/bento-collector/collector/internal/fixture"
)

func TestStoreVersions(t *testing.T) {
	st, err := NewFileStore(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	c := fixture.CPUCollector()
	for i := 0; i < 3; i++ {
		c.Mapping.Unit = "u" + strconv.Itoa(i)
		if _, err := st.SaveCollector(c); err != nil {
			t.Fatal(err)
		}
	}
	latest, _ := st.GetCollector(c.ID, 0)
	first, _ := st.GetCollector(c.ID, 1)
	if latest.Version != 3 || first.Mapping.Unit != "u0" {
		t.Fatalf("latest=%d first=%s", latest.Version, first.Mapping.Unit)
	}
	if _, err := st.GetCollector("../etc", 0); err == nil {
		t.Fatal("path traversal must be refused")
	}
}
