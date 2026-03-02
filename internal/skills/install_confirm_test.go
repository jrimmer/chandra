package skills

import "testing"

func TestBuildInstallConfirmation(t *testing.T) {
	pm := &ManualManager{}
	confirm, err := BuildInstallConfirmation(pm, "gh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if confirm.PackageName != "gh" {
		t.Errorf("expected package name gh, got %q", confirm.PackageName)
	}
	if confirm.Source != "manual" {
		t.Errorf("expected source manual, got %q", confirm.Source)
	}
	if confirm.Command == "" {
		t.Error("expected non-empty command")
	}
}

func TestInstallConfirmation_Format(t *testing.T) {
	confirm := &InstallConfirmation{
		PackageName: "gh",
		Version:     "2.40.0",
		Description: "GitHub CLI",
		Source:      "brew",
		Command:     "brew install gh",
		Effects:     "Download ~15MB, add gh to /usr/local/bin",
	}
	formatted := confirm.Format()
	if formatted == "" {
		t.Error("expected non-empty formatted output")
	}
}
