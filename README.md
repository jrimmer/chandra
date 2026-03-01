# Chandra

A personal AI agent runtime with structured memory, event-driven triggers, and Discord integration.

## Prerequisites

- Go 1.22+
- CGO enabled (C compiler required: gcc or clang)
- SQLite3 development headers

## Build

    CGO_ENABLED=1 go build ./...

## Configuration

Copy the example config and edit:

    mkdir -p ~/.config/chandra
    chmod 700 ~/.config/chandra
    # Create ~/.config/chandra/config.toml with your settings
    chmod 600 ~/.config/chandra/config.toml

Minimum config:

    [provider]
    type = "anthropic"
    api_key = "sk-ant-..."
    model = "claude-sonnet-4-6"

    [embeddings]
    base_url = "https://api.openai.com/v1"
    api_key = "sk-..."
    model = "text-embedding-3-small"

    [channels.discord]
    token = "Bot ..."
    channel_ids = ["your-channel-id"]

## Run

    CGO_ENABLED=1 go run ./cmd/chandrad

## CLI Commands

    chandra health            # Health check
    chandra memory search <q> # Search memory
    chandra intent list       # List active intents
    chandra intent add <desc> # Add intent
    chandra log --today       # Today's action log
    chandra confirm <id>      # Approve a pending tool call

## Testing

    CGO_ENABLED=1 go test ./...

## Architecture

See DESIGN.md for full architecture documentation.
