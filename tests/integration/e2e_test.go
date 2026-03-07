// e2e_test.go — end-to-end pipeline tests using the loopback channel and stub
// provider. Tests drive the full agent.AgentLoop without touching Discord, a
// real LLM, or the network.
//
// Shared infrastructure from agent_loop_test.go (same package):
//   openTestDB, fixedEmbedder, noopExecutor, noopRegistry, budgetIntentAdapter
package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/actionlog"
	"github.com/jrimmer/chandra/internal/agent"
	"github.com/jrimmer/chandra/internal/budget"
	"github.com/jrimmer/chandra/internal/channels"
	"github.com/jrimmer/chandra/internal/channels/loopback"
	"github.com/jrimmer/chandra/internal/memory"
	"github.com/jrimmer/chandra/internal/memory/episodic"
	"github.com/jrimmer/chandra/internal/memory/identity"
	"github.com/jrimmer/chandra/internal/memory/intent"
	"github.com/jrimmer/chandra/internal/memory/semantic"
	"github.com/jrimmer/chandra/internal/provider"
	"github.com/jrimmer/chandra/internal/provider/stub"
	"github.com/jrimmer/chandra/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── harness ──────────────────────────────────────────────────────────────────

type e2eHarness struct {
	loop    agent.AgentLoop
	ch      *loopback.Channel
	prov    *stub.Provider
	ppDone  chan struct{}
	sessMgr agent.Manager
}

func newHarness(t *testing.T, chanID string, prov *stub.Provider) *e2eHarness {
	t.Helper()
	s := openTestDB(t)
	db := s.DB()

	epStore := episodic.NewStore(db)
	semStore, err := semantic.NewStore(db, &fixedEmbedder{})
	require.NoError(t, err)
	intentStore := intent.NewStore(db)
	mem := memory.New(epStore, semStore, intentStore, identity.NewStore(db, "e2e-user"))

	aLog, err := actionlog.NewLog(db)
	require.NoError(t, err)
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	ch := loopback.New(chanID, 32)
	ppDone := make(chan struct{}, 1)

	loop := agent.NewLoop(agent.LoopConfig{
		Provider:        prov,
		Memory:          mem,
		Budget:          budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore}),
		Registry:        reg,
		Executor:        tools.NewExecutor(reg, db, 5*time.Second),
		ActionLog:       aLog,
		Channel:         ch,
		MaxRounds:       5,
		PostProcessDone: func() { select { case ppDone <- struct{}{}: default: } },
	})

	sessMgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)

	return &e2eHarness{loop: loop, ch: ch, prov: prov, ppDone: ppDone, sessMgr: sessMgr}
}

// run sends one message, waits for post-processing, returns the LLM response.
func (h *e2eHarness) run(t *testing.T, msg channels.InboundMessage) string {
	t.Helper()
	ctx := context.Background()
	convID := agent.ComputeConversationID(msg.ChannelID, msg.UserID)
	sess, err := h.sessMgr.GetOrCreate(ctx, convID, msg.ChannelID, msg.UserID)
	require.NoError(t, err)
	msg.ConversationID = sess.ConversationID
	resp, err := h.loop.Run(ctx, sess, msg)
	require.NoError(t, err)
	select {
	case <-h.ppDone:
	case <-time.After(5 * time.Second):
		t.Fatal("post-processing timeout")
	}
	return resp
}

func (h *e2eHarness) waitPP(t *testing.T) {
	t.Helper()
	select {
	case <-h.ppDone:
	case <-time.After(5 * time.Second):
		t.Fatal("post-processing timeout")
	}
}

// ─── loopback channel unit tests ──────────────────────────────────────────────

func TestLoopback_SendCapturesOutbound(t *testing.T) {
	ch := loopback.New("ch", 8)
	id, err := ch.Send(context.Background(), channels.OutboundMessage{ChannelID: "ch", Content: "hello"})
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	sent := ch.DrainSent()
	require.Len(t, sent, 1)
	assert.Equal(t, "hello", sent[0].Content)
}

func TestLoopback_ReactCapturesReactions(t *testing.T) {
	ch := loopback.New("ch", 8)
	require.NoError(t, ch.React(context.Background(), "msg-1", "👍"))
	reacts := ch.DrainReactions()
	require.Len(t, reacts, 1)
	assert.Equal(t, "msg-1", reacts[0].MessageID)
	assert.Equal(t, "👍", reacts[0].Emoji)
}

func TestLoopback_EditCapturesEdits(t *testing.T) {
	ch := loopback.New("ch", 8)
	require.NoError(t, ch.Edit(context.Background(), "ch", "msg-2", "updated"))
	edits := ch.DrainEdits()
	require.Len(t, edits, 1)
	assert.Equal(t, "msg-2", edits[0].MessageID)
	assert.Equal(t, "updated", edits[0].Content)
}

func TestLoopback_DrainEmpty(t *testing.T) {
	ch := loopback.New("ch", 8)
	assert.Nil(t, ch.DrainSent())
	assert.Nil(t, ch.DrainReactions())
	assert.Nil(t, ch.DrainEdits())
}

func TestLoopback_MultipleMessages(t *testing.T) {
	ch := loopback.New("ch", 8)
	for i := range 3 {
		_, _ = ch.Send(context.Background(), channels.OutboundMessage{Content: strings.Repeat("x", i+1)})
	}
	sent := ch.DrainSent()
	assert.Len(t, sent, 3)
}

// ─── stub provider unit tests ─────────────────────────────────────────────────

func TestStub_SingleResponse(t *testing.T) {
	p := stub.NewProvider("pong")
	resp, err := p.Complete(context.Background(), provider.CompletionRequest{})
	require.NoError(t, err)
	assert.Equal(t, "pong", resp.Message.Content)
	assert.Equal(t, 1, p.CallCount())
}

func TestStub_SequenceRepeatLast(t *testing.T) {
	p := stub.NewSequenceProvider("a", "b")
	for range 4 {
		_, _ = p.Complete(context.Background(), provider.CompletionRequest{})
	}
	resp, _ := p.Complete(context.Background(), provider.CompletionRequest{})
	assert.Equal(t, "b", resp.Message.Content, "last response should repeat after sequence exhausted")
}

func TestStub_ErrorPropagated(t *testing.T) {
	p := &stub.Provider{ModelName: "err", Err: context.DeadlineExceeded}
	_, err := p.Complete(context.Background(), provider.CompletionRequest{})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStub_ToolProvider_FirstCallIsToolCall(t *testing.T) {
	p := stub.NewToolProvider("my_tool", []byte(`{}`), "done")
	resp, err := p.Complete(context.Background(), provider.CompletionRequest{})
	require.NoError(t, err)
	assert.Equal(t, "tool_calls", resp.StopReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "my_tool", resp.ToolCalls[0].Name)
}

func TestStub_LastRequest(t *testing.T) {
	p := stub.NewProvider("ok")
	req := provider.CompletionRequest{Messages: []provider.Message{{Role: "user", Content: "hello"}}}
	_, _ = p.Complete(context.Background(), req)
	last := p.LastRequest()
	require.Len(t, last.Messages, 1)
	assert.Equal(t, "hello", last.Messages[0].Content)
}

func TestStub_EmbeddingProvider(t *testing.T) {
	ep := stub.NewEmbeddingProvider(768)
	resp, err := ep.Embed(context.Background(), provider.EmbeddingRequest{Texts: []string{"a", "b"}})
	require.NoError(t, err)
	assert.Len(t, resp.Embeddings, 2)
	assert.Len(t, resp.Embeddings[0], 768)
	assert.Equal(t, float32(0.1), resp.Embeddings[0][0])
}

// ─── full pipeline e2e tests ──────────────────────────────────────────────────

// TestE2E_BasicRoundTrip: one message in → correct response out.
func TestE2E_BasicRoundTrip(t *testing.T) {
	prov := stub.NewProvider("Pong!")
	h := newHarness(t, "e2e-01", prov)

	resp := h.run(t, channels.InboundMessage{
		ID: "m1", ChannelID: "e2e-01", UserID: "e2e-user", Content: "ping",
	})
	assert.Equal(t, "Pong!", resp)
	assert.Equal(t, 1, prov.CallCount())

	var found bool
	for _, m := range prov.LastRequest().Messages {
		if m.Role == "user" && strings.Contains(m.Content, "ping") {
			found = true
		}
	}
	assert.True(t, found, "user message must appear in LLM prompt")
}

// TestE2E_ReplyContext_BotTurn reproduces the conversational-disconnect fix:
// "Is this still an issue?" replying to a bot message about a timestamp bug
// must inject the bot's prior message as an assistant turn immediately before
// the user's reply in the LLM prompt.
func TestE2E_ReplyContext_BotTurn(t *testing.T) {
	prov := stub.NewProvider("Yes, fixed.")
	h := newHarness(t, "e2e-02", prov)

	prior := "I still have that conversation timestamp bug - dates showing year ~58,000 AD."
	h.run(t, channels.InboundMessage{
		ID:                "m2",
		ChannelID:         "e2e-02",
		UserID:            "e2e-user",
		Content:           "Is this still an issue?",
		ReferencedContent: prior,
		ReferencedRole:    "assistant",
	})

	msgs := prov.LastRequest().Messages
	refIdx, userIdx := -1, -1
	for i, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.Content, "58,000 AD") {
			refIdx = i
		}
		if m.Role == "user" && strings.Contains(m.Content, "Is this still an issue") {
			userIdx = i
		}
	}
	assert.GreaterOrEqual(t, refIdx, 0, "referenced bot message must appear in LLM prompt")
	assert.GreaterOrEqual(t, userIdx, 0, "user reply must appear in LLM prompt")
	assert.Equal(t, refIdx+1, userIdx, "referenced message must immediately precede the user reply")
}

// TestE2E_ReplyContext_UserTurn: human-authored referenced message injected as user turn.
func TestE2E_ReplyContext_UserTurn(t *testing.T) {
	prov := stub.NewProvider("Got it.")
	h := newHarness(t, "e2e-03", prov)

	h.run(t, channels.InboundMessage{
		ID:                "m3",
		ChannelID:         "e2e-03",
		UserID:            "e2e-user",
		Content:           "yeah that's what I said",
		ReferencedContent: "Can you fix the timestamp bug?",
		ReferencedRole:    "user",
	})

	var found bool
	for _, m := range prov.LastRequest().Messages {
		if m.Role == "user" && strings.Contains(m.Content, "Can you fix") {
			found = true
		}
	}
	assert.True(t, found, "user-authored reference must appear as a user turn in LLM prompt")
}

// TestE2E_NoReplyContext_ExactlyOneUserTurn: no extra injection on plain messages.
func TestE2E_NoReplyContext_ExactlyOneUserTurn(t *testing.T) {
	prov := stub.NewProvider("Sure.")
	h := newHarness(t, "e2e-04", prov)

	h.run(t, channels.InboundMessage{
		ID: "m4", ChannelID: "e2e-04", UserID: "e2e-user", Content: "just a message",
	})

	userCount := 0
	for _, m := range prov.LastRequest().Messages {
		if m.Role == "user" {
			userCount++
		}
	}
	assert.Equal(t, 1, userCount, "exactly 1 user turn for a plain message (no reply context)")
}

// TestE2E_SequentialTurns: second turn must see first turn's episode in context.
func TestE2E_SequentialTurns(t *testing.T) {
	prov := stub.NewSequenceProvider("First.", "Second.")
	h := newHarness(t, "e2e-05", prov)

	ctx := context.Background()
	convID := agent.ComputeConversationID("e2e-05", "e2e-user")

	for _, content := range []string{"turn one", "turn two"} {
		sess, err := h.sessMgr.GetOrCreate(ctx, convID, "e2e-05", "e2e-user")
		require.NoError(t, err)
		_, err = h.loop.Run(ctx, sess, channels.InboundMessage{
			ID:             "seq-" + content,
			ConversationID: sess.ConversationID,
			ChannelID:      "e2e-05",
			UserID:         "e2e-user",
			Content:        content,
		})
		require.NoError(t, err)
		h.waitPP(t)
	}

	assert.Equal(t, 2, prov.CallCount())
	var foundFirstTurn bool
	for _, m := range prov.Calls[1].Messages {
		if strings.Contains(m.Content, "turn one") {
			foundFirstTurn = true
		}
	}
	assert.True(t, foundFirstTurn, "second turn must see first turn in episodic context")
}

// TestE2E_ToolCallLoop: single tool-call round — emit tool, execute, return text.
func TestE2E_ToolCallLoop(t *testing.T) {
	prov := stub.NewToolProvider("echo", []byte(`{"text":"hello"}`), "Tool said: hello")
	h := newHarness(t, "e2e-06", prov)

	resp := h.run(t, channels.InboundMessage{
		ID: "m6", ChannelID: "e2e-06", UserID: "e2e-user", Content: "call echo",
	})

	assert.Equal(t, "Tool said: hello", resp)
	assert.Equal(t, 2, prov.CallCount(), "two LLM calls: tool dispatch + final answer")
}

// TestE2E_EpisodicPersistence: user + assistant episodes written to DB after Run().
func TestE2E_EpisodicPersistence(t *testing.T) {
	ctx := context.Background()

	s := openTestDB(t)
	db := s.DB()
	epStore := episodic.NewStore(db)
	semStore, err := semantic.NewStore(db, &fixedEmbedder{})
	require.NoError(t, err)
	intentStore := intent.NewStore(db)
	mem := memory.New(epStore, semStore, intentStore, identity.NewStore(db, "e2e-user"))
	aLog, err := actionlog.NewLog(db)
	require.NoError(t, err)
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	prov := stub.NewProvider("Stored!")
	ch := loopback.New("e2e-07", 8)
	ppDone := make(chan struct{}, 1)
	loop := agent.NewLoop(agent.LoopConfig{
		Provider:        prov,
		Memory:          mem,
		Budget:          budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore}),
		Registry:        reg,
		Executor:        tools.NewExecutor(reg, db, 5*time.Second),
		ActionLog:       aLog,
		Channel:         ch,
		MaxRounds:       5,
		PostProcessDone: func() { select { case ppDone <- struct{}{}: default: } },
	})
	sessMgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)
	convID := agent.ComputeConversationID("e2e-07", "e2e-user")
	sess, err := sessMgr.GetOrCreate(ctx, convID, "e2e-07", "e2e-user")
	require.NoError(t, err)

	_, err = loop.Run(ctx, sess, channels.InboundMessage{
		ID: "m7", ConversationID: sess.ConversationID,
		ChannelID: "e2e-07", UserID: "e2e-user", Content: "remember this",
	})
	require.NoError(t, err)
	select {
	case <-ppDone:
	case <-time.After(5 * time.Second):
		t.Fatal("post-processing timeout")
	}

	eps, err := epStore.Recent(ctx, sess.ID, 10)
	require.NoError(t, err)
	require.Len(t, eps, 2, "expected user + assistant episodes")
	roles := map[string]bool{}
	for _, ep := range eps {
		roles[ep.Role] = true
	}
	assert.True(t, roles["user"], "user episode must be persisted")
	assert.True(t, roles["assistant"], "assistant episode must be persisted")
}

// TestE2E_ProviderError_NoPanic: provider failure must not panic.
func TestE2E_ProviderError_NoPanic(t *testing.T) {
	ctx := context.Background()
	s := openTestDB(t)
	db := s.DB()
	semStore, err := semantic.NewStore(db, &fixedEmbedder{})
	require.NoError(t, err)
	intentStore := intent.NewStore(db)
	mem := memory.New(episodic.NewStore(db), semStore, intentStore, identity.NewStore(db, "e2e-user"))
	aLog, err := actionlog.NewLog(db)
	require.NoError(t, err)
	reg, err := tools.NewRegistry(nil)
	require.NoError(t, err)

	failProv := &stub.Provider{ModelName: "fail", Err: context.DeadlineExceeded}
	ch := loopback.New("e2e-08", 8)
	ppDone := make(chan struct{}, 1)

	loop := agent.NewLoop(agent.LoopConfig{
		Provider:        failProv,
		Memory:          mem,
		Budget:          budget.New(0.5, 0.3, 0.2, 24, &budgetIntentAdapter{store: intentStore}),
		Registry:        reg,
		Executor:        tools.NewExecutor(reg, db, 5*time.Second),
		ActionLog:       aLog,
		Channel:         ch,
		MaxRounds:       5,
		PostProcessDone: func() { select { case ppDone <- struct{}{}: default: } },
	})
	sessMgr, err := agent.NewManager(db, 30*time.Minute)
	require.NoError(t, err)
	convID := agent.ComputeConversationID("e2e-08", "e2e-user")
	sess, err := sessMgr.GetOrCreate(ctx, convID, "e2e-08", "e2e-user")
	require.NoError(t, err)

	resp, runErr := loop.Run(ctx, sess, channels.InboundMessage{
		ID: "m8", ConversationID: sess.ConversationID,
		ChannelID: "e2e-08", UserID: "e2e-user", Content: "will fail",
	})

	// Must not panic — reaching this line is success.
	if runErr == nil {
		assert.NotEmpty(t, resp, "graceful fallback text expected on provider error")
	}
}
