package skills

import "testing"

func TestDetectPackageManager(t *testing.T) {
	pm := DetectPackageManager()
	if pm == nil {
		t.Fatal("expected a package manager to be detected")
	}
	name := pm.Name()
	valid := map[string]bool{"apt": true, "brew": true, "dnf": true, "pacman": true, "manual": true}
	if !valid[name] {
		t.Errorf("unexpected package manager: %q", name)
	}
}

func TestManualManager_IsInstalled(t *testing.T) {
	mm := &ManualManager{}
	if mm.IsInstalled("nonexistent_binary_xyz") {
		t.Error("expected false for nonexistent binary")
	}
	if !mm.IsInstalled("ls") {
		t.Error("expected true for ls")
	}
}

func TestManualManager_Search(t *testing.T) {
	mm := &ManualManager{}
	results, err := mm.Search("nonexistent_xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %v", results)
	}
}
