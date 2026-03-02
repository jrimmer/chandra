package integration

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jrimmer/chandra/internal/skills"
)

func skillsTestdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "skills")
}

func TestIntegration_SkillRegistry_LoadAndMatch(t *testing.T) {
	reg := skills.NewRegistry()
	err := reg.Load(context.Background(), skillsTestdataDir(t), nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	all := reg.All()
	if len(all) == 0 {
		t.Fatal("expected at least one skill loaded")
	}

	// Match by trigger keyword.
	matches := reg.Match("what is the weather in London")
	if len(matches) == 0 {
		t.Error("expected weather skill to match")
	}

	// No match for unrelated message.
	matches = reg.Match("run database migrations")
	if len(matches) != 0 {
		t.Errorf("expected no match, got %d", len(matches))
	}
}
