package setup

import (
	"encoding/json"
	"os"
)

// Checkpoint tracks progress of the init wizard so it can be resumed.
// Secrets (API keys, bot tokens) are never stored here — they are re-prompted
// on resume. Non-secret values are stored so the wizard can skip re-collecting
// them from the user if the stage already completed.
type Checkpoint struct {
	ProviderDone    bool   `json:"provider_done"`
	ProviderType    string `json:"provider_type"`     // non-secret: "openai", "anthropic", etc.
	ProviderModel   string `json:"provider_model"`    // non-secret: model name
	ProviderBaseURL string `json:"provider_base_url"` // non-secret: custom/openrouter base URL (empty for hosted)

	ChannelsDone     bool     `json:"channels_done"`
	DiscordChannelID string   `json:"discord_channel_id"` // non-secret: Discord channel snowflake
	AccessPolicy     string   `json:"access_policy"`      // non-secret: "invite", "role", etc.
	DiscordRoleIDs   []string `json:"discord_role_ids"`   // non-secret: role ID list for role policy

	IdentityDone     bool   `json:"identity_done"`
	AgentName        string `json:"agent_name"`        // non-secret
	AgentDescription string `json:"agent_description"` // non-secret

	ConfigWritten bool `json:"config_written"`

	// FreshStart records that the user explicitly chose "Fresh start" or "Start over"
	// so that a resumed session still archives the old config before writing the new one.
	FreshStart bool `json:"fresh_start"`
}

// SaveCheckpoint writes the checkpoint to path atomically.
// Writes to a .tmp sibling first, then renames, so a crash mid-write
// always leaves either the old checkpoint or the new one intact — never a
// truncated file. This matters because a corrupt checkpoint is treated as
// "no checkpoint" (see LoadCheckpoint + corrupt-checkpoint handling in Run),
// and a crash between os.Rename(config→.bak) and the FreshStart=false save
// would otherwise leave the install broken with no live config.
//
// Note: os.WriteFile + os.Rename is not fully durable against power failure
// without fsync + parent-dir sync. For a setup wizard where the worst case
// is "re-run init" and the old config is preserved in .bak, this trade-off
// is acceptable. Full fsync durability is reserved for the database layer.
func SaveCheckpoint(path string, cp *Checkpoint) error {
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCheckpoint reads a checkpoint from path.
// Returns nil (not an error) if the file does not exist.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// DeleteCheckpoint removes the checkpoint file after successful init.
func DeleteCheckpoint(path string) error {
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}
