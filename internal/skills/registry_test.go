package skills

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

func TestRegistry_Load(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(context.Background(), testdataDir(t), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "github" should be loaded (ls is available).
	all := reg.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 skill loaded, got %d", len(all))
	}
	if all[0].Name != "github" {
		t.Errorf("expected github, got %q", all[0].Name)
	}

	// "broken" should be unmet.
	unmet := reg.Unmet()
	if len(unmet) != 1 {
		t.Fatalf("expected 1 unmet skill, got %d", len(unmet))
	}
	if unmet[0].Name != "broken" {
		t.Errorf("expected broken, got %q", unmet[0].Name)
	}
}

func TestRegistry_Get(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Load(context.Background(), testdataDir(t), nil)

	skill, ok := reg.Get("github")
	if !ok {
		t.Fatal("expected github skill to be found")
	}
	if skill.Description != "GitHub operations via gh CLI" {
		t.Errorf("unexpected description: %q", skill.Description)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent skill to not be found")
	}
}

func TestRegistry_Match(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Load(context.Background(), testdataDir(t), nil)

	// Should match on trigger keyword.
	matches := reg.Match("I need to create a pull request")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Name != "github" {
		t.Errorf("expected github match, got %q", matches[0].Name)
	}

	// Should not match unrelated message.
	matches = reg.Match("what is the weather today")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestRegistry_Match_CaseInsensitive(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Load(context.Background(), testdataDir(t), nil)

	matches := reg.Match("check GITHUB actions")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestRegistry_Reload(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Load(context.Background(), testdataDir(t), nil)

	err := reg.Reload(context.Background())
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	// Should still have the same skills.
	all := reg.All()
	if len(all) != 1 || all[0].Name != "github" {
		t.Errorf("unexpected skills after reload: %v", all)
	}
}

func TestRegistry_Load_IgnoresToolsGo(t *testing.T) {
	// The registry should warn (but not fail) when tools.go exists in a user skill dir.
	// This test verifies Load still succeeds with test data that happens to have no tools.go.
	reg := NewRegistry()
	err := reg.Load(context.Background(), testdataDir(t), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegistry_ApprovalWorkflow(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Load(context.Background(), testdataDir(t), nil)

	generated := Skill{
		Name:     "docker",
		Triggers: []string{"docker", "container"},
		Content:  "# Docker Skill",
		Generated: &GeneratedMeta{
			By:     "chandra",
			Date:   time.Now(),
			Source: "docker --help exploration",
			Status: SkillPendingReview,
		},
	}
	err := reg.Register(generated)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	// Should appear in PendingReview but NOT in Match results.
	pending := reg.PendingReview()
	if len(pending) != 1 || pending[0].Name != "docker" {
		t.Errorf("expected docker in pending, got %v", pending)
	}

	matches := reg.Match("start a docker container")
	for _, m := range matches {
		if m.Name == "docker" {
			t.Error("pending skill should not appear in Match results")
		}
	}

	// Approve it.
	err = reg.Approve("docker", "sal")
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	// Now it should match.
	matches = reg.Match("start a docker container")
	found := false
	for _, m := range matches {
		if m.Name == "docker" {
			found = true
		}
	}
	if !found {
		t.Error("approved skill should appear in Match results")
	}

	// PendingReview should be empty.
	if len(reg.PendingReview()) != 0 {
		t.Error("expected no pending skills after approval")
	}
}

func TestRegistry_Reject(t *testing.T) {
	reg := NewRegistry()
	generated := Skill{
		Name:     "bad-skill",
		Triggers: []string{"bad-skill"},
		Generated: &GeneratedMeta{
			Status: SkillPendingReview,
		},
	}
	_ = reg.Register(generated)

	err := reg.Reject("bad-skill", "sal")
	if err != nil {
		t.Fatalf("reject failed: %v", err)
	}

	// Should not appear in any listing.
	matches := reg.Match("bad-skill")
	if len(matches) != 0 {
		t.Error("rejected skill should not match")
	}
}
