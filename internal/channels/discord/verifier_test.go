package discord_test

import (
	"testing"

	"github.com/jrimmer/chandra/internal/channels/discord"
)

func TestVerifier_OptionsDefaults(t *testing.T) {
	opts := discord.DefaultVerifyOptions()
	if opts.TimeoutSec != 120 {
		t.Errorf("expected 120s timeout, got %d", opts.TimeoutSec)
	}
	if opts.Message == "" {
		t.Error("default message should not be empty")
	}
}
