package git

import "testing"

func TestRefSafety(t *testing.T) {
	for _, good := range []string{"origin", "main", "feature/login", "user@fork"} {
		if !validRef(good) {
			t.Errorf("expected valid ref %q", good)
		}
	}
	for _, bad := range []string{"--force", "+main", "main:other", "name with space", ""} {
		if validRef(bad) {
			t.Errorf("expected invalid ref %q", bad)
		}
	}
}
