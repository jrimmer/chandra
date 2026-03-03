package setup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jrimmer/chandra/internal/setup"
)

func TestCheckpoint_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".init-checkpoint.json")

	cp := &setup.Checkpoint{
		ProviderDone: true,
		ChannelsDone: false,
		IdentityDone: false,
	}

	if err := setup.SaveCheckpoint(path, cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := setup.LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !loaded.ProviderDone {
		t.Error("expected ProviderDone=true")
	}
	if loaded.ChannelsDone {
		t.Error("expected ChannelsDone=false")
	}
}

func TestCheckpoint_DeleteOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".init-checkpoint.json")

	cp := &setup.Checkpoint{ProviderDone: true}
	if err := setup.SaveCheckpoint(path, cp); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := setup.DeleteCheckpoint(path); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected checkpoint file to be deleted")
	}
}
